package database

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
	"wwfc/common"
	"wwfc/logging"

	"github.com/logrusorgru/aurora/v3"
)

// $1 is a JSON array of the requesting client's NG device IDs. The recursive
// CTE expands it into the full set of device IDs transitively linked through
// shared accounts (device-ID ban evasion detection), mirroring the original
// Postgres array-overlap (`&&` / unnest) query against the user_ng_device_ids
// junction table. ng_device_id is reconstructed per row via json_group_array.
const (
	knownNgDeviceIdsCTE = `
	WITH RECURSIVE known AS (
		SELECT CAST(value AS INTEGER) AS device_id FROM json_each($1)
		UNION
		SELECT d2.device_id
		FROM known k
		JOIN user_ng_device_ids d1 ON d1.device_id = k.device_id
		JOIN users u ON u.profile_id = d1.profile_id AND u.allow_default_keys = 0
		JOIN user_ng_device_ids d2 ON d2.profile_id = u.profile_id
	)`

	SearchUserBan = knownNgDeviceIdsCTE + `
	SELECT u.has_ban, u.ban_tos,
	       (SELECT json_group_array(device_id) FROM user_ng_device_ids d WHERE d.profile_id = u.profile_id),
	       COALESCE(u.ban_reason, '')
	FROM users u
	WHERE u.has_ban = 1
	  AND (u.profile_id = $2
		OR (u.allow_default_keys = 0 AND EXISTS (
			SELECT 1 FROM user_ng_device_ids d JOIN known k ON k.device_id = d.device_id WHERE d.profile_id = u.profile_id))
		OR ($3 != '' AND u.last_ip_address = $3)
		OR ($4 != '' AND u.last_ip_address = $4))
	  AND (u.ban_expires IS NULL OR u.ban_expires > $5)
	ORDER BY u.ban_tos DESC LIMIT 1`

	SearchUserBanInfo = knownNgDeviceIdsCTE + `
	SELECT u.has_ban, u.ban_tos, u.ban_issued, u.ban_expires, COALESCE(u.ban_reason, ''),
	       (SELECT json_group_array(device_id) FROM user_ng_device_ids d WHERE d.profile_id = u.profile_id),
	       u.profile_id, u.gsbrcd, u.last_ingamesn
	FROM users u
	WHERE u.has_ban = 1
	  AND (u.profile_id = $2
		OR (u.allow_default_keys = 0 AND EXISTS (
			SELECT 1 FROM user_ng_device_ids d JOIN known k ON k.device_id = d.device_id WHERE d.profile_id = u.profile_id))
		OR ($3 != '' AND u.last_ip_address = $3)
		OR ($4 != '' AND u.last_ip_address = $4))
	ORDER BY u.ban_expires DESC LIMIT 1`
)

var (
	ErrDeviceIDMismatch   = errors.New("NG device ID mismatch")
	ErrProhibitedDeviceID = errors.New("used prohibited NG device ID in request")
	ErrProfileBannedTOS   = errors.New("profile is banned for violating the Terms of Service")
)

func canUseAdditionalDeviceID(policy string, lastIPAddress *string, ipAddress string) bool {
	switch policy {
	case "always":
		return true
	case "SameIPAddress":
		return lastIPAddress != nil && ipAddress == *lastIPAddress
	default:
		return false
	}
}

func (c *Connection) LoginUserToGPCM(userId uint64, gsbrcd string, profileId uint32, defaultKey bool, ngDeviceId uint32, ipAddress string, ingamesn string, deviceAuth bool) (User, error) {
	var exists bool
	err := c.pool.QueryRow(c.ctx, DoesUserExist, userId, gsbrcd).Scan(&exists)
	if err != nil {
		return User{}, err
	}

	user := User{
		UserId:   userId,
		GsbrCode: gsbrcd,
	}

	var lastIPAddress *string

	if !exists {
		user.ProfileId = profileId
		user.NgDeviceId = []uint32{ngDeviceId}
		if ngDeviceId == 0 {
			user.NgDeviceId = []uint32{}
		}
		user.UniqueNick = common.Base32Encode(userId) + gsbrcd
		user.Email = user.UniqueNick + "@nds"

		// Create the GPCM account
		err := c.CreateUser(&user)
		if err != nil {
			logging.Error("DATABASE", "Error creating user:", aurora.Cyan(userId), aurora.Cyan(gsbrcd), aurora.Cyan(user.ProfileId), "\nerror:", err.Error())
			return User{}, err
		}

		logging.Notice("DATABASE", "Created new GPCM user:", aurora.Cyan(userId), aurora.Cyan(gsbrcd), aurora.Cyan(user.ProfileId))
		user.Created = true
	} else {
		var firstName *string
		var lastName *string
		var allowDefaultKeys bool

		err := c.pool.QueryRow(c.ctx, GetUserProfileID, userId, gsbrcd).Scan(&user.ProfileId, &user.Email, &user.UniqueNick, &firstName, &lastName, &user.OpenHost, &lastIPAddress, &allowDefaultKeys)
		if err != nil {
			return User{}, err
		}
		user.NgDeviceId = c.getNgDeviceIds(user.ProfileId)

		if defaultKey && !allowDefaultKeys && !common.GetConfig().AllowDefaultDolphinKeys {
			return User{}, ErrProhibitedDeviceID
		}

		if firstName != nil {
			user.FirstName = *firstName
		}

		if lastName != nil {
			user.LastName = *lastName
		}

		validDeviceId := false
		deviceIdList := ""
		for index, id := range user.NgDeviceId {
			if id == ngDeviceId {
				validDeviceId = true
			}

			if !validDeviceId && id == 0 {
				// Replace the 0 with the actual device ID
				user.NgDeviceId[index] = ngDeviceId
				err = c.setNgDeviceIds(user.ProfileId, user.NgDeviceId)
				validDeviceId = true
			}

			deviceIdList += aurora.Cyan(fmt.Sprintf("%08x", id)).String() + ", "
		}

		if !validDeviceId && ngDeviceId != 0 {
			if len(user.NgDeviceId) > 0 && !canUseAdditionalDeviceID(common.GetConfig().AllowMultipleDeviceIDs, lastIPAddress, ipAddress) {
				logging.Error("DATABASE", "NG device ID mismatch for profile", aurora.Cyan(user.ProfileId), "- expected one of {", deviceIdList[:len(deviceIdList)-2], "} but got", aurora.Cyan(fmt.Sprintf("%08x", ngDeviceId)))
				return User{}, ErrDeviceIDMismatch
			}

			if len(user.NgDeviceId) > 0 {
				logging.Warn("DATABASE", "Adding NG device ID", aurora.Cyan(fmt.Sprintf("%08x", ngDeviceId)), "to profile", aurora.Cyan(user.ProfileId))
			}

			user.NgDeviceId = append(user.NgDeviceId, ngDeviceId)
			err = c.setNgDeviceIds(user.ProfileId, user.NgDeviceId)
		} else if deviceAuth && !validDeviceId && ngDeviceId == 0 {
			if len(user.NgDeviceId) > 0 && !common.GetConfig().AllowConnectWithoutDeviceID {
				logging.Error("DATABASE", "NG device ID not provided for profile", aurora.Cyan(user.ProfileId), "- expected one of {", deviceIdList[:len(deviceIdList)-2], "} but got", aurora.Cyan("00000000"))
				return User{}, ErrDeviceIDMismatch
			}
		}

		if err != nil {
			return User{}, err
		}

		if profileId != 0 && user.ProfileId != profileId {
			err := c.UpdateProfileID(&user, profileId)
			if err != nil {
				logging.Warn("DATABASE", "Could not update", aurora.Cyan(userId), aurora.Cyan(gsbrcd), "profile ID from", aurora.Cyan(user.ProfileId), "to", aurora.Cyan(profileId))
			} else {
				logging.Notice("DATABASE", "Updated GPCM user profile ID:", aurora.Cyan(userId), aurora.Cyan(gsbrcd), aurora.Cyan(user.ProfileId))
			}
		}

		logging.Notice("DATABASE", "Log in GPCM user:", aurora.Cyan(userId), aurora.Cyan(user.GsbrCode), "-", aurora.Cyan(user.ProfileId))
	}

	// This should be set if the user already knows its own profile ID
	if profileId != 0 && user.LastName == "" {
		c.UpdateProfile(&user, map[string]string{
			"lastname": "000000000" + gsbrcd,
		})
	}

	// Update the user's last IP address and ingamesn
	if deviceAuth {
		_, err = c.pool.Exec(c.ctx, UpdateUserLastIPAddress, user.ProfileId, ipAddress, ingamesn)
		if err != nil {
			return User{}, err
		}
	}

	emptyString := ""
	if lastIPAddress == nil {
		lastIPAddress = &emptyString
	}

	// Find ban from device ID or IP address
	var banExists bool
	var banTOS bool
	var bannedDeviceIdJSON []byte
	var banReason string
	timeNow := time.Now().UTC()
	err = c.pool.QueryRow(c.ctx, SearchUserBan, deviceIdsToJSON(user.NgDeviceId), user.ProfileId, ipAddress, *lastIPAddress, timeNow).Scan(&banExists, &banTOS, &bannedDeviceIdJSON, &banReason)
	bannedDeviceIdList := parseDeviceIdsJSON(bannedDeviceIdJSON)

	if err != nil {
		if err != sql.ErrNoRows {
			return User{}, err
		}

		banExists = false
	}

	if banExists {
		// Find first device ID in common
		bannedDeviceId := uint32(0)
		for _, id := range bannedDeviceIdList {
			for _, id2 := range user.NgDeviceId {
				if id == id2 {
					bannedDeviceId = id
					break
				}
			}

			if bannedDeviceId != 0 {
				break
			}
		}

		if bannedDeviceId == 0 && len(bannedDeviceIdList) > 0 {
			bannedDeviceId = bannedDeviceIdList[len(bannedDeviceIdList)-1]
		}

		if banTOS {
			logging.Warn("DATABASE", "Profile", aurora.Cyan(user.ProfileId), "is banned")
			return User{RestrictedDeviceId: bannedDeviceId, BanReason: banReason}, ErrProfileBannedTOS
		}

		logging.Warn("DATABASE", "Profile", aurora.Cyan(user.ProfileId), "is restricted")
		user.Restricted = true
		user.RestrictedDeviceId = bannedDeviceId
		user.BanReason = banReason
	}

	return user, nil
}

func (c *Connection) LoginUserToGameStats(userId uint64, gsbrcd string) (User, error) {
	user := User{
		UserId:   userId,
		GsbrCode: gsbrcd,
	}

	var firstName *string
	var lastName *string
	var lastIPAddress *string
	var allowDefaultKeys bool

	err := c.pool.QueryRow(c.ctx, GetUserProfileID, userId, gsbrcd).Scan(&user.ProfileId, &user.Email, &user.UniqueNick, &firstName, &lastName, &user.OpenHost, &lastIPAddress, &allowDefaultKeys)
	if err != nil {
		return User{}, err
	}
	user.NgDeviceId = c.getNgDeviceIds(user.ProfileId)

	if firstName != nil {
		user.FirstName = *firstName
	}

	if lastName != nil {
		user.LastName = *lastName
	}

	return user, nil
}

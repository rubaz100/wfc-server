package database

import (
	"encoding/json"
	"errors"
	"math/rand"
	"time"
)

const (
	InsertUser              = `INSERT INTO users (user_id, gsbrcd, password, email, unique_nick) VALUES ($1, $2, $3, $4, $5)`
	InsertUserWithProfileID = `INSERT INTO users (profile_id, user_id, gsbrcd, password, email, unique_nick) VALUES ($1, $2, $3, $4, $5, $6)`
	UpdateUserTable         = `UPDATE users SET firstname = CASE WHEN $3 THEN $2 ELSE firstname END, lastname = CASE WHEN $5 THEN $4 ELSE lastname END, open_host = CASE WHEN $7 THEN $6 ELSE open_host END WHERE profile_id = $1`
	UpdateUserProfileID     = `UPDATE users SET profile_id = $3 WHERE user_id = $1 AND gsbrcd = $2`
	GetUser                 = `SELECT user_id, gsbrcd, email, unique_nick, firstname, lastname, open_host, last_ip_address, last_ingamesn FROM users WHERE profile_id = $1`
	ClearProfileQuery       = `DELETE FROM users WHERE profile_id = $1 RETURNING user_id, gsbrcd, email, unique_nick, firstname, lastname, open_host, last_ip_address, last_ingamesn`
	DoesUserExist           = `SELECT EXISTS(SELECT 1 FROM users WHERE user_id = $1 AND gsbrcd = $2)`
	IsProfileIDInUse        = `SELECT EXISTS(SELECT 1 FROM users WHERE profile_id = $1)`
	DeleteUserSession       = `DELETE FROM sessions WHERE profile_id = $1`
	GetUserProfileID        = `SELECT profile_id, email, unique_nick, firstname, lastname, open_host, last_ip_address, allow_default_keys FROM users WHERE user_id = $1 AND gsbrcd = $2`
	UpdateUserLastIPAddress = `UPDATE users SET last_ip_address = $2, last_ingamesn = $3 WHERE profile_id = $1`
	UpdateUserBan           = `UPDATE users SET has_ban = 1, ban_issued = $2, ban_expires = $3, ban_reason = $4, ban_reason_hidden = $5, ban_moderator = $6, ban_tos = $7 WHERE profile_id = $1`
	DisableUserBan          = `UPDATE users SET has_ban = 0 WHERE profile_id = $1`

	selectNgDeviceIds = `SELECT device_id FROM user_ng_device_ids WHERE profile_id = $1 ORDER BY device_id`
	insertNgDeviceId  = `INSERT OR IGNORE INTO user_ng_device_ids (profile_id, device_id) VALUES ($1, $2)`
	deleteNgDeviceIds = `DELETE FROM user_ng_device_ids WHERE profile_id = $1`
)

type User struct {
	ProfileId          uint32
	UserId             uint64
	GsbrCode           string
	NgDeviceId         []uint32
	Email              string
	UniqueNick         string
	FirstName          string
	LastName           string
	Restricted         bool
	RestrictedDeviceId uint32
	BanReason          string
	OpenHost           bool
	LastInGameSn       string
	LastIPAddress      string
	Created            bool
}

var (
	ErrProfileIDInUse         = errors.New("profile ID is already in use")
	ErrReservedProfileIDRange = errors.New("profile ID is in reserved range")
)

// getNgDeviceIds returns the NG device IDs associated with a profile.
func (c *Connection) getNgDeviceIds(profileId uint32) []uint32 {
	rows, err := c.pool.Query(c.ctx, selectNgDeviceIds, profileId)
	if err != nil {
		return []uint32{}
	}
	defer rows.Close()

	ids := []uint32{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return ids
		}
		ids = append(ids, uint32(id))
	}
	return ids
}

// setNgDeviceIds replaces the full set of NG device IDs for a profile. A device
// ID of 0 is a placeholder and is not persisted.
func (c *Connection) setNgDeviceIds(profileId uint32, ids []uint32) error {
	if _, err := c.pool.Exec(c.ctx, deleteNgDeviceIds, profileId); err != nil {
		return err
	}
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, err := c.pool.Exec(c.ctx, insertNgDeviceId, profileId, id); err != nil {
			return err
		}
	}
	return nil
}

func (c *Connection) CreateUser(user *User) error {
	if user.ProfileId == 0 {
		res, err := c.pool.Exec(c.ctx, InsertUser, user.UserId, user.GsbrCode, "", user.Email, user.UniqueNick)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		user.ProfileId = uint32(id)
		return c.setNgDeviceIds(user.ProfileId, user.NgDeviceId)
	}

	if user.ProfileId >= 1000000000 {
		return ErrReservedProfileIDRange
	}

	var exists bool
	err := c.pool.QueryRow(c.ctx, IsProfileIDInUse, user.ProfileId).Scan(&exists)
	if err != nil {
		return err
	}

	if exists {
		return ErrProfileIDInUse
	}

	_, err = c.pool.Exec(c.ctx, InsertUserWithProfileID, user.ProfileId, user.UserId, user.GsbrCode, "", user.Email, user.UniqueNick)
	if err != nil {
		return err
	}
	return c.setNgDeviceIds(user.ProfileId, user.NgDeviceId)
}

func (c *Connection) UpdateProfileID(user *User, newProfileId uint32) error {
	if newProfileId >= 1000000000 {
		return ErrReservedProfileIDRange
	}

	var exists bool
	err := c.pool.QueryRow(c.ctx, IsProfileIDInUse, newProfileId).Scan(&exists)
	if err != nil {
		return err
	}

	if exists {
		return ErrProfileIDInUse
	}

	_, err = c.pool.Exec(c.ctx, UpdateUserProfileID, user.UserId, user.GsbrCode, newProfileId)
	if err == nil {
		user.ProfileId = newProfileId
	}

	return err
}

func GetUniqueUserID() uint64 {
	// Not guaranteed unique but doesn't matter in practice if multiple people have the same user ID.
	return uint64(rand.Int63n(0x80000000000))
}

func (c *Connection) UpdateProfile(user *User, data map[string]string) {
	firstName, firstNameExists := data["firstname"]
	lastName, lastNameExists := data["lastname"]
	openHost, openHostExists := data["wl:oh"]
	openHostBool := openHostExists && openHost != "0"

	_, err := c.pool.Exec(c.ctx, UpdateUserTable, user.ProfileId, firstName, firstNameExists, lastName, lastNameExists, openHostBool, openHostExists)
	if err != nil {
		panic(err)
	}

	if firstNameExists {
		user.FirstName = firstName
	}

	if lastNameExists {
		user.LastName = lastName
	}

	if openHostExists {
		user.OpenHost = openHostBool
	}
}

func (c *Connection) GetProfile(profileId uint32) (User, bool) {
	user := User{}
	row := c.pool.QueryRow(c.ctx, GetUser, profileId)
	err := row.Scan(&user.UserId, &user.GsbrCode, &user.Email, &user.UniqueNick, &user.FirstName, &user.LastName, &user.OpenHost, &user.LastIPAddress, &user.LastInGameSn)
	if err != nil {
		return User{}, false
	}

	user.ProfileId = profileId
	return user, true
}

func (c *Connection) ClearProfile(profileId uint32) (User, bool) {
	user := User{}
	row := c.pool.QueryRow(c.ctx, ClearProfileQuery, profileId)
	err := row.Scan(&user.UserId, &user.GsbrCode, &user.Email, &user.UniqueNick, &user.FirstName, &user.LastName, &user.OpenHost, &user.LastIPAddress, &user.LastInGameSn)

	if err != nil {
		return User{}, false
	}

	user.ProfileId = profileId
	return user, true
}

func (c *Connection) BanUser(profileId uint32, tos bool, length time.Duration, reason string, reasonHidden string, moderator string) bool {
	_, err := c.pool.Exec(c.ctx, UpdateUserBan, profileId, time.Now().UTC(), time.Now().UTC().Add(length), reason, reasonHidden, moderator, tos)
	return err == nil
}

func (c *Connection) UnbanUser(profileId uint32) bool {
	_, err := c.pool.Exec(c.ctx, DisableUserBan, profileId)
	return err == nil
}

func (c *Connection) SearchUserBan(profileId uint32, ngDeviceId uint32, ipAddress string, lastIpAddress string) (
	tos bool, issued time.Time, expires time.Time, reason string, bannedProfileId uint32, gsbrCode string, inGameName string, err error) {
	row := c.pool.QueryRow(c.ctx, SearchUserBanInfo, deviceIdsToJSON([]uint32{ngDeviceId}), profileId, ipAddress, lastIpAddress)
	var hasBan bool
	var bannedNgDeviceIdJSON []byte
	err = row.Scan(&hasBan, &tos, &issued, &expires, &reason, &bannedNgDeviceIdJSON, &bannedProfileId, &gsbrCode, &inGameName)
	if err == nil && !hasBan {
		err = errors.New("no ban found")
	}
	if len(gsbrCode) > 4 {
		gsbrCode = gsbrCode[:4]
	}
	return tos, issued, expires, reason, bannedProfileId, gsbrCode, inGameName, err
}

// deviceIdsToJSON renders NG device IDs as a JSON array for use with SQLite's
// json_each(). Zero placeholders are dropped. The result is always a valid JSON
// array (at minimum "[]").
func deviceIdsToJSON(ids []uint32) string {
	out := make([]uint32, 0, len(ids))
	for _, id := range ids {
		if id != 0 {
			out = append(out, id)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// parseDeviceIdsJSON parses a JSON array of device IDs (as produced by SQLite's
// json_group_array) back into a slice.
func parseDeviceIdsJSON(data []byte) []uint32 {
	if len(data) == 0 {
		return nil
	}
	var ids []uint32
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil
	}
	return ids
}

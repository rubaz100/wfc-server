package database

import "time"

const ListBansQuery = `
	SELECT profile_id, gsbrcd, last_ingamesn, ban_issued, ban_expires,
	       ban_reason, ban_reason_hidden, ban_moderator, ban_tos,
	       (SELECT json_group_array(device_id) FROM user_ng_device_ids d WHERE d.profile_id = users.profile_id),
	       last_ip_address
	FROM users
	WHERE has_ban = 1
	ORDER BY ban_issued DESC`

type BanRecord struct {
	ProfileID     uint32
	GsbrCode      string
	InGameName    string
	Issued        *time.Time
	Expires       *time.Time
	Reason        string
	ReasonHidden  string
	Moderator     string
	TOS           bool
	NgDeviceIds   []uint32
	LastIPAddress string
}

// ListBans returns all users that currently have an active ban flag.
func (c *Connection) ListBans() ([]BanRecord, error) {
	rows, err := c.pool.Query(c.ctx, ListBansQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BanRecord
	for rows.Next() {
		var rec BanRecord
		var gsbr, name, reason, reasonHidden, moderator, lastIP *string
		var tos *bool
		var ngIDsJSON []byte

		err := rows.Scan(
			&rec.ProfileID, &gsbr, &name, &rec.Issued, &rec.Expires,
			&reason, &reasonHidden, &moderator, &tos,
			&ngIDsJSON, &lastIP,
		)
		if err != nil {
			return nil, err
		}
		rec.NgDeviceIds = parseDeviceIdsJSON(ngIDsJSON)

		if gsbr != nil {
			rec.GsbrCode = *gsbr
		}
		if name != nil {
			rec.InGameName = *name
		}
		if reason != nil {
			rec.Reason = *reason
		}
		if reasonHidden != nil {
			rec.ReasonHidden = *reasonHidden
		}
		if moderator != nil {
			rec.Moderator = *moderator
		}
		if lastIP != nil {
			rec.LastIPAddress = *lastIP
		}
		if tos != nil {
			rec.TOS = *tos
		}

		out = append(out, rec)
	}

	return out, rows.Err()
}

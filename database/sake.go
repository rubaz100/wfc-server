package database

import (
	"encoding/json"
	"errors"
	"math/rand"
	"strings"
	"wwfc/common"
	"wwfc/filter"
)

const (
	SakeFieldTypeByte          = 0
	SakeFieldTypeShort         = 1
	SakeFieldTypeInt           = 2
	SakeFieldTypeFloat         = 3
	SakeFieldTypeAsciiString   = 4
	SakeFieldTypeUnicodeString = 5
	SakeFieldTypeBoolean       = 6
	SakeFieldTypeDateAndTime   = 7
	SakeFieldTypeBinaryData    = 8
	SakeFieldTypeInt64         = 9
)

// sakeMaxFields mirrors the upstream Postgres CHECK that limited a record to 64
// fields. SQLite has no equivalent jsonb path check, so it is enforced in Go.
const sakeMaxFields = 64

type SakeFieldType int

type SakeField struct {
	Type  SakeFieldType `json:"type"`
	Value string        `json:"value"`
}

type SakeRecord struct {
	GameId   int
	OwnerId  int32
	TableId  string
	RecordId int32
	Fields   map[string]SakeField
}

const (
	// $3 and $4 are JSON arrays of owner IDs / record IDs (empty array = no filter).
	getSakeRecordsQuery = `
		SELECT owner_id, record_id, fields
		FROM sake_records
		WHERE game_id = $1
		  AND table_id = $2
		  AND (json_array_length($4) = 0 OR record_id IN (SELECT value FROM json_each($4)))
		  AND (json_array_length($3) = 0 OR owner_id IN (SELECT value FROM json_each($3)))`

	updateSakeRecordQuery = `
        UPDATE sake_records
        SET
            fields = CASE WHEN owner_id = $4 THEN json_patch(fields, $5) ELSE fields END,
            update_time = CASE WHEN owner_id = $4 THEN CURRENT_TIMESTAMP ELSE update_time END
        WHERE game_id = $1
          AND table_id = $2
          AND record_id = $3
        RETURNING owner_id`

	insertSakeRecordQuery = `
		INSERT INTO sake_records (game_id, table_id, record_id, owner_id, fields)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING record_id`

	deleteSakeRecordQuery = `
		DELETE FROM sake_records
		WHERE game_id = $1
		  AND table_id = $2
		  AND record_id = $3
		  AND owner_id = $4`

	checkMaxSakeRecordsQuery = `
		SELECT COUNT(*)
		FROM sake_records
		WHERE owner_id = $1`
)

var (
	ErrSakeNotOwned           = errors.New("record is not owned by the specified owner ID")
	ErrSakeFieldLimitExceeded = errors.New("record has too many fields")
)

var _ = common.MaybeUnused(deleteSakeRecordQuery)

func parseSakeFieldsFromJson(fieldsJson []byte) (map[string]SakeField, error) {
	var fields map[string]SakeField
	err := json.Unmarshal(fieldsJson, &fields)
	if err != nil {
		return nil, err
	}

	return fields, nil
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func int32SliceToJSON(v []int32) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func (c *Connection) GetSakeRecords(gameId int, ownerIds []int32, tableId string, recordIds []int32, fields []string, filterExpr string) ([]SakeRecord, error) {
	if fields == nil {
		fields = []string{}
	}
	common.MaybeUnused(fields)
	if ownerIds == nil {
		ownerIds = []int32{}
	}
	if recordIds == nil {
		recordIds = []int32{}
	}

	query := getSakeRecordsQuery
	if filterExpr != "" {
		tree, err := filter.Parse(filterExpr)
		if err != nil {
			return nil, err
		}

		filterQuery, err := createSqlFilter(tree)
		if err != nil {
			return nil, err
		}

		// This filter has been entirely rewritten by our filter code,
		// based on the expression supplied by the user. This should be safe!!!
		query += " AND (" + filterQuery + ")"
	}

	rows, err := c.pool.Query(c.ctx, query, gameId, tableId, int32SliceToJSON(ownerIds), int32SliceToJSON(recordIds))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []SakeRecord
	for rows.Next() {
		record := SakeRecord{
			GameId:  gameId,
			TableId: tableId,
		}
		var fieldsJson []byte
		if err := rows.Scan(&record.OwnerId, &record.RecordId, &fieldsJson); err != nil {
			return nil, err
		}
		fields, err := parseSakeFieldsFromJson(fieldsJson)
		if err != nil {
			return nil, err
		}
		record.Fields = fields

		records = append(records, record)
	}

	return records, rows.Err()
}

func (c *Connection) UpdateSakeRecord(record SakeRecord, ownerId int32) error {
	if len(record.Fields) > sakeMaxFields {
		return ErrSakeFieldLimitExceeded
	}

	fieldsJson, err := json.Marshal(record.Fields)
	if err != nil {
		return err
	}
	var existingOwnerId int32
	err = c.pool.QueryRow(c.ctx, updateSakeRecordQuery, record.GameId, record.TableId, record.RecordId, ownerId, fieldsJson).Scan(&existingOwnerId)
	if err != nil {
		return err
	}
	if ownerId != 0 && existingOwnerId != ownerId {
		return ErrSakeNotOwned
	}

	return nil
}

func (c *Connection) InsertSakeRecord(record SakeRecord) (recordId int32, err error) {
	if len(record.Fields) > sakeMaxFields {
		return 0, ErrSakeFieldLimitExceeded
	}

	fieldsJson, err := json.Marshal(record.Fields)
	if err != nil {
		return 0, err
	}

	for i := 0; i < 10; i++ {
		// record_id was a random DEFAULT in Postgres; generate it here so the
		// INSERT is portable. Positive int32 to satisfy the column's domain.
		candidate := rand.Int31n(2147483647) + 1
		err = c.pool.QueryRow(c.ctx, insertSakeRecordQuery, record.GameId, record.TableId, candidate, record.OwnerId, fieldsJson).Scan(&recordId)
		if err == nil {
			break
		}
		// Retry if the random record ID collided with an existing one.
		if !isUniqueViolation(err) {
			break
		}
	}
	return recordId, err
}

func (c *Connection) IsMaxSakeRecordsReached(profileId uint32, maxRecords int) (bool, error) {
	var count int
	err := c.pool.QueryRow(c.ctx, checkMaxSakeRecordsQuery, profileId).Scan(&count)
	if err != nil {
		return false, err
	}
	return count >= maxRecords, nil
}

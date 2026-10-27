package database

import (
	"path/filepath"
	"testing"
	"time"
	"wwfc/common"
)

// TestSmokeSQLite exercises the risky parts of the Postgres->SQLite port against
// a real on-disk SQLite database: autoincrement seeding, the ng_device_id
// junction table, the recursive device-graph ban search, and SAKE json_patch.
func TestSmokeSQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "smoke.db")
	cfg := common.Config{DatabasePath: dbPath}
	c := Start(cfg)
	defer c.Close()

	// 1. profile_id autoincrement starts at 1000000000.
	userA := &User{UserId: 111, GsbrCode: "RMCPAAAA", Email: "a@nds", UniqueNick: "a", NgDeviceId: []uint32{0xAAAA}}
	if err := c.CreateUser(userA); err != nil {
		t.Fatalf("CreateUser A: %v", err)
	}
	if userA.ProfileId != 1000000000 {
		t.Fatalf("first profile_id = %d, want 1000000000", userA.ProfileId)
	}

	userB := &User{UserId: 222, GsbrCode: "RMCPBBBB", Email: "b@nds", UniqueNick: "b", NgDeviceId: []uint32{0xAAAA, 0xBBBB}}
	if err := c.CreateUser(userB); err != nil {
		t.Fatalf("CreateUser B: %v", err)
	}
	if userB.ProfileId != 1000000001 {
		t.Fatalf("second profile_id = %d, want 1000000001", userB.ProfileId)
	}

	// 2. ng_device_id junction round-trips.
	got := c.getNgDeviceIds(userB.ProfileId)
	if len(got) != 2 || got[0] != 0xAAAA || got[1] != 0xBBBB {
		t.Fatalf("getNgDeviceIds = %v, want [AAAA BBBB]", got)
	}

	// 3. Ban userA (TOS), then search by a device only userB shares with userA
	//    (0xAAAA). The recursive device-graph CTE should surface userA's ban.
	if !c.BanUser(userA.ProfileId, true, time.Hour, "cheating", "", "mod") {
		t.Fatal("BanUser A failed")
	}
	var banExists, banTOS bool
	var bannedJSON []byte
	var banReason string
	err := c.pool.QueryRow(c.ctx, SearchUserBan,
		deviceIdsToJSON([]uint32{0xBBBB}), uint32(0), "", "", time.Now().UTC(),
	).Scan(&banExists, &banTOS, &bannedJSON, &banReason)
	if err != nil {
		t.Fatalf("SearchUserBan: %v", err)
	}
	if !banExists || !banTOS || banReason != "cheating" {
		t.Fatalf("ban search = exists:%v tos:%v reason:%q, want true/true/cheating", banExists, banTOS, banReason)
	}
	linked := parseDeviceIdsJSON(bannedJSON)
	if len(linked) == 0 {
		t.Fatalf("expected banned device ids, got none")
	}

	// 4. SAKE: insert, get, then merge via json_patch.
	rec := SakeRecord{
		GameId: 1687, TableId: "FriendInfo", OwnerId: int32(userB.ProfileId),
		Fields: map[string]SakeField{"info": {Type: SakeFieldTypeBinaryData, Value: "hello"}},
	}
	rid, err := c.InsertSakeRecord(rec)
	if err != nil {
		t.Fatalf("InsertSakeRecord: %v", err)
	}
	if rid <= 0 {
		t.Fatalf("record id = %d, want > 0", rid)
	}

	if info := c.GetMKWFriendInfo(userB.ProfileId); info != "hello" {
		t.Fatalf("GetMKWFriendInfo = %q, want hello", info)
	}

	// Update merges a new value in via json_patch.
	rec.RecordId = rid
	rec.Fields["info"] = SakeField{Type: SakeFieldTypeBinaryData, Value: "world"}
	if err := c.UpdateSakeRecord(rec, int32(userB.ProfileId)); err != nil {
		t.Fatalf("UpdateSakeRecord: %v", err)
	}
	if info := c.GetMKWFriendInfo(userB.ProfileId); info != "world" {
		t.Fatalf("after update GetMKWFriendInfo = %q, want world", info)
	}

	// 5. GetProfile / ClearProfile cascade. firstname/lastname are set during a
	//    real GPCM login (updatepro); set them here so the plain-string scan in
	//    GetUser has non-NULL values, matching the live flow.
	c.UpdateProfile(userB, map[string]string{"firstname": "First", "lastname": "Last"})
	if _, ok := c.GetProfile(userB.ProfileId); !ok {
		t.Fatal("GetProfile B not found")
	}
	if _, ok := c.ClearProfile(userB.ProfileId); !ok {
		t.Fatal("ClearProfile B failed")
	}
	if ids := c.getNgDeviceIds(userB.ProfileId); len(ids) != 0 {
		t.Fatalf("device ids after ClearProfile = %v, want cascade-deleted", ids)
	}
}

package net

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"zjcs/server/internal/game"
	"zjcs/server/internal/store"
)

// configDir 优先使用线上实际加载的 app/configs（含草药点位），回退到源码内的 configs_shared。
func configDir(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"../../../configs", "../../configs_shared"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Fatal("找不到配置目录（app/configs 或 server/configs_shared）")
	return ""
}

func setupQuestTest(t *testing.T) *store.DiskStore {
	t.Helper()
	if err := game.Init(configDir(t)); err != nil {
		t.Fatal(err)
	}
	d, err := store.NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := disk
	disk = d
	t.Cleanup(func() {
		_ = d.FlushAll()
		disk = old
	})
	return d
}

func newQuestTestPlayer(t *testing.T, d *store.DiskStore, uid string, level int) *store.PlayerState {
	t.Helper()
	p := store.NewPlayer(uid, uid, "warrior")
	p.Appearance.Created = true
	p.Core.Level = level
	p.Core.HP = 1000000
	p.World.WorldID = "W1"
	if err := d.Save(uid, p); err != nil {
		t.Fatal(err)
	}
	return p
}

func questRequest(t *testing.T, handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	// 线上 v1.9.92 的 mustUser 依赖鉴权 token，这里按请求体 userId 签一个同密钥 token。
	var probe struct {
		UserID string `json:"userId"`
	}
	_ = json.Unmarshal([]byte(body), &probe)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if probe.UserID != "" {
		req.Header.Set("Authorization", "Bearer "+signToken(probe.UserID))
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func dailyValue(p *store.PlayerState, kind string) int {
	daily, _ := p.Ext["dailyQuests"].(map[string]any)
	if daily == nil {
		return 0
	}
	progress, _ := daily["progress"].(map[string]any)
	return int(numAny(progress, kind, 0))
}

func TestDailyRewardAtLevel(t *testing.T) {
	base := questReward{Gold: 800, Gem: 10, Exp: 200, Mat: 2}
	tests := []struct {
		level int
		want  questReward
	}{
		{level: 1, want: questReward{Gold: 800, Gem: 10, Exp: 200, Mat: 2}},
		{level: 10, want: questReward{Gold: 1520, Gem: 10, Exp: 380, Mat: 2}},
		{level: 60, want: questReward{Gold: 5520, Gem: 10, Exp: 1380, Mat: 2}},
	}
	for _, test := range tests {
		if got := dailyRewardAtLevel(base, test.level); got != test.want {
			t.Errorf("level %d reward = %+v, want %+v", test.level, got, test.want)
		}
	}
}

func TestDailyQuestClaimAndMidnightReset(t *testing.T) {
	beforeMidnight := time.Date(2026, 11, 28, 23, 59, 59, 0, time.Local).Unix()
	afterMidnight := time.Date(2026, 11, 29, 0, 0, 1, 0, time.Local).Unix()
	p := &store.PlayerState{Ext: map[string]any{"quests": map[string]any{"q_kill3": true}}}
	p.Core.Level = 10
	for i := 0; i < 4; i++ {
		advanceDailyQuest(p, "battle", beforeMidnight)
	}
	if _, err := claimDailyQuest(p, "d_battle5", beforeMidnight); err == nil {
		t.Fatal("4/5 progress must not be claimable")
	}
	advanceDailyQuest(p, "battle", beforeMidnight)
	rw, err := claimDailyQuest(p, "d_battle5", beforeMidnight)
	if err != nil {
		t.Fatal(err)
	}
	if rw.Gold != 1520 || rw.Exp != 380 || rw.Gem != 10 {
		t.Fatalf("unexpected level 10 reward: %+v", rw)
	}
	if _, err = claimDailyQuest(p, "d_battle5", beforeMidnight); err == nil {
		t.Fatal("duplicate daily claim must fail")
	}

	daily, changed := ensureDailyQuests(p, afterMidnight)
	if !changed {
		t.Fatal("midnight rollover must replace daily state")
	}
	if got := int(numAny(daily["progress"].(map[string]any), "battle", 0)); got != 0 {
		t.Fatalf("progress after rollover = %d, want 0", got)
	}
	if len(daily["claimed"].(map[string]any)) != 0 {
		t.Fatal("claimed state must reset after midnight")
	}
	quests := p.Ext["quests"].(map[string]any)
	if quests["q_kill3"] != true {
		t.Fatal("daily reset must preserve permanent main quest claims")
	}
	if _, err = claimDailyQuest(p, "d_battle5", afterMidnight); err == nil {
		t.Fatal("new day still requires new progress")
	}
	if _, err = claimDailyQuest(p, "unknown", afterMidnight); err == nil {
		t.Fatal("unknown daily quest must fail")
	}
}

func TestDailyQuestJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.Local).Unix()
	p := &store.PlayerState{Ext: map[string]any{}}
	advanceDailyQuest(p, "chest", now)
	advanceDailyQuest(p, "chest", now)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var restored store.PlayerState
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	advanceDailyQuest(&restored, "chest", now)
	if got := dailyValue(&restored, "chest"); got != 3 {
		t.Fatalf("progress after JSON round trip = %d, want 3", got)
	}
	if _, err = claimDailyQuest(&restored, "d_chest2", now); err != nil {
		t.Fatalf("claim after JSON round trip: %v", err)
	}
}

func TestQuestRewardCompatibility(t *testing.T) {
	oldRewards := map[string]questReward{
		"q_kill3": {800, 30, 300, 0}, "q_chest1": {600, 20, 200, 2},
		"q_equip4": {900, 40, 400, 0}, "q_enhance1": {700, 25, 250, 1},
		"q_travel": {1500, 80, 800, 5},
	}
	for id, want := range oldRewards {
		if got := questRewards[id]; got != want {
			t.Errorf("old reward %s = %+v, want %+v", id, got, want)
		}
	}
	for _, id := range []string{"q2_eva_talk", "q2_herb3", "q2_w2_win5", "q2_w2_chest2", "q2_enhance1", "q2_travel_w3"} {
		if _, ok := questRewards[id]; !ok {
			t.Errorf("missing chapter 2 reward %s", id)
		}
	}
}

func TestDailyQuestHTTPClaimAndDuplicateGuard(t *testing.T) {
	d := setupQuestTest(t)
	p := newQuestTestPlayer(t, d, "u_daily_http", 10)
	p.Core.Gold, p.Core.Gem, p.Core.Material, p.Core.Exp = 1000, 0, 0, 0
	now := game.ServerNow()
	for i := 0; i < 3; i++ {
		advanceDailyQuest(p, "herb", now)
	}
	// DiskStore 缓存字节而非指针，内存改动必须显式落盘，HTTP 处理函数 loadOrCreate 才能读到。
	if err := d.Save("u_daily_http", p); err != nil {
		t.Fatal(err)
	}
	rec := questRequest(t, playerAction, `{"userId":"u_daily_http","action":"quest_claim","questId":"d_herb3"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Reward map[string]int    `json:"reward"`
		State  store.PlayerState `json:"state"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"gold": 760, "gem": 5, "exp": 190, "material": 1}
	if !reflect.DeepEqual(payload.Reward, want) {
		t.Fatalf("reward = %+v, want %+v", payload.Reward, want)
	}
	if payload.State.Core.Gold != 1760 || payload.State.Core.Gem != 5 || payload.State.Core.Material != 1 {
		t.Fatalf("resources after claim: %+v", payload.State.Core)
	}

	rec = questRequest(t, playerAction, `{"userId":"u_daily_http","action":"quest_claim","questId":"d_herb3"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409", rec.Code)
	}
	after, err := d.Load("u_daily_http")
	if err != nil {
		t.Fatal(err)
	}
	if after.Core.Gold != 1760 || after.Core.Gem != 5 || after.Core.Material != 1 {
		t.Fatal("duplicate request changed resources")
	}
	rec = questRequest(t, playerAction, `{"userId":"u_daily_http","action":"quest_claim","questId":"not_a_quest"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown quest status = %d, want 400", rec.Code)
	}
}

func TestDailyBattleSuccessAndFailure(t *testing.T) {
	d := setupQuestTest(t)
	newQuestTestPlayer(t, d, "u_daily_win", 60)
	// W1_M05 为线上 v1.9.92 存在的 W1 大世界 MOB 点位
	winBody := `{"userId":"u_daily_win","bd":["w_slash"],"monsterId":"slime","worldId":"W1","nodeId":"W1_M05"}`
	rec := questRequest(t, battleFight, winBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("winning battle status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var winPayload struct {
		Report game.BattleReport `json:"report"`
		State  store.PlayerState `json:"state"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&winPayload); err != nil {
		t.Fatal(err)
	}
	if winPayload.Report.Result != "win" {
		t.Fatalf("level 60 battle result = %s, want win", winPayload.Report.Result)
	}
	if got := dailyValue(&winPayload.State, "battle"); got != 1 {
		t.Fatalf("win progress = %d, want 1", got)
	}

	loser := newQuestTestPlayer(t, d, "u_daily_loss", 1)
	loser.Core.HP = 1
	// 线上 W1 无 BOSS 点位，改为无点位单怪结算，强制与 boss_beetle 对战
	lossBody := `{"userId":"u_daily_loss","bd":["w_guard"],"monsterId":"boss_beetle"}`
	rec = questRequest(t, battleFight, lossBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("losing battle status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var lossPayload struct {
		Report game.BattleReport `json:"report"`
		State  store.PlayerState `json:"state"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&lossPayload); err != nil {
		t.Fatal(err)
	}
	if lossPayload.Report.Result == "win" {
		t.Fatal("level 1 / 1 HP boss battle unexpectedly won")
	}
	if got := dailyValue(&lossPayload.State, "battle"); got != 0 {
		t.Fatalf("loss progress = %d, want 0", got)
	}
}

func TestDailyChestAndHerbAuthority(t *testing.T) {
	d := setupQuestTest(t)
	newQuestTestPlayer(t, d, "u_daily_world", 10)
	spots := game.SM().ActiveSpots("W1", "u_daily_world")
	var chestID, potionID string
	for _, spot := range spots {
		if spot.Type == "CHEST" && chestID == "" {
			chestID = spot.NodeID
		}
		if spot.Type == "POTION" && potionID == "" {
			potionID = spot.NodeID
		}
	}
	if chestID == "" || potionID == "" {
		t.Fatalf("missing test spots: chest=%q potion=%q", chestID, potionID)
	}

	// 冷却基线来自线上配置 worlds.json 的 respawn 段：
	// chestBaseSec=0 表示宝箱开箱即刷新（重复开箱成功）；potionBaseSec=120 有冷却。
	// 仅当配置了冷却时，重复操作才应返回 409 且不推进每日进度。
	chestCD := int(numAny(game.C().Worlds.Respawn, "chestBaseSec", 300))
	potionCD := int(numAny(game.C().Worlds.Respawn, "potionBaseSec", 120))

	body := fmt.Sprintf(`{"userId":"u_daily_world","worldId":"W1","nodeId":%q}`, chestID)
	rec := questRequest(t, chestOpen, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("chest status = %d, body = %s", rec.Code, rec.Body.String())
	}
	p, _ := d.Load("u_daily_world")
	if got := dailyValue(p, "chest"); got != 1 {
		t.Fatalf("chest progress = %d, want 1", got)
	}
	rec = questRequest(t, chestOpen, body)
	if chestCD > 0 {
		if rec.Code != http.StatusConflict {
			t.Fatalf("duplicate chest open with cooldown status = %d, want 409", rec.Code)
		}
		p2, _ := d.Load("u_daily_world")
		if got := dailyValue(p2, "chest"); got != 1 {
			t.Fatalf("duplicate chest open must not advance progress, got %d", got)
		}
	} else {
		// chestBaseSec=0：开箱即刷新，重复开箱成功且进度继续推进
		if rec.Code != http.StatusOK {
			t.Fatalf("duplicate chest open (no cooldown) status = %d, body = %s", rec.Code, rec.Body.String())
		}
		p2, _ := d.Load("u_daily_world")
		if got := dailyValue(p2, "chest"); got != 2 {
			t.Fatalf("no-cooldown duplicate chest open should advance progress to 2, got %d", got)
		}
	}

	body = fmt.Sprintf(`{"userId":"u_daily_world","worldId":"W1","nodeId":%q}`, potionID)
	rec = questRequest(t, potionPick, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("potion status = %d, body = %s", rec.Code, rec.Body.String())
	}
	p, _ = d.Load("u_daily_world")
	if got := dailyValue(p, "herb"); got != 1 {
		t.Fatalf("herb progress = %d, want 1", got)
	}
	rec = questRequest(t, potionPick, body)
	if potionCD > 0 {
		if rec.Code != http.StatusConflict {
			t.Fatalf("duplicate herb pick with cooldown status = %d, want 409", rec.Code)
		}
		p2, _ := d.Load("u_daily_world")
		if got := dailyValue(p2, "herb"); got != 1 {
			t.Fatalf("failed duplicate herb pick must not advance progress, got %d", got)
		}
	} else {
		if rec.Code != http.StatusOK {
			t.Fatalf("duplicate herb pick (no cooldown) status = %d, body = %s", rec.Code, rec.Body.String())
		}
		p2, _ := d.Load("u_daily_world")
		if got := dailyValue(p2, "herb"); got != 2 {
			t.Fatalf("no-cooldown duplicate herb pick should advance progress to 2, got %d", got)
		}
	}
}

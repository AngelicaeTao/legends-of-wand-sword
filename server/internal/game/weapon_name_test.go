package game

import "testing"

// v1.9.38：武器两套命名（战斗掉落/开箱/角色面板共用）
func TestWeaponNaming(t *testing.T) {
	if err := Init("../../configs_shared"); err != nil { t.Fatal(err) }
	cases := []struct{ cls string; q int }{
		{"warrior", 0}, {"warrior", 2}, {"warrior", 4}, {"mage", 0}, {"mage", 3},
	}
	for _, c := range cases {
		n := WeaponName(c.cls, c.q, "weapon")
		if n == "" { t.Fatalf("武器命名缺失: %+v", c) }
		t.Logf("%s系 q%d → %s系·%s", WeaponClassCN(c.cls), c.q, WeaponClassCN(c.cls), n)
	}
	// 非武器不带系名
	if WeaponName("warrior", 2, "helmet") != "" {
		t.Fatal("头盔不应取武器名")
	}
	// 跨系拒绝信息
	wc := WeaponClassFor("sage")
	if wc != "mage" { t.Fatalf("sage 应属法师系，实际 %s", wc) }
	if wc = WeaponClassFor("knight"); wc != "warrior" { t.Fatalf("knight 应属战士系，实际 %s", wc) }
}

#!/usr/bin/env bash
# 端到端冒烟测试：验证权威服务 + 存档闭环。
# 前置：先在另一终端运行 `bash scripts/start.sh`（或 go run main.go）。
set -euo pipefail
BASE="${BASE:-http://127.0.0.1:8080}"
UID_TEST="smoke_$(date +%s)"

jget(){ python3 -c "import json,sys; d=json.load(sys.stdin); print(eval('d'+'$1'))"; }
post(){ curl -s -X POST "$BASE$1" -H 'Content-Type: application/json' -d "$2"; }

echo "=== 0) 健康检查 ==="
post "" '{}' >/dev/null 2>&1 || true
curl -s "$BASE/health" | python3 -m json.tool

echo "=== 1) 创角并落库 ==="
post /api/character/new "{\"userId\":\"$UID_TEST\",\"name\":\"冒烟测试\",\"classId\":\"mage\"}" \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print('  Lv',d['core']['level'],'金币',d['core']['gold'],'职业',d['core']['classId'])"

echo "=== 2) 战斗 + 奖励落库 ==="
post /api/battle/fight "{\"userId\":\"$UID_TEST\",\"bd\":[\"fireball\",\"arcane_bolt\",\"heal\",\"frost_nova\"],\"monsterId\":\"slime\",\"monsterLv\":1}" \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print('  结果',d['report']['result'],'入账',d['logs'],'经验',d['state']['core']['exp'])"

echo "=== 3) 离线结算（回拨 lastSeen 到 10h 前，应封顶 8h）==="
NOW=$(date +%s); OLD=$((NOW-36000))
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
python3 - "$ROOT" "$UID_TEST" "$OLD" <<'PY'
import json,sys,os
root,uid,old=sys.argv[1],sys.argv[2],int(sys.argv[3])
p=os.path.join(root,"data","saves",f"{uid}.json")
if os.path.exists(p):
    d=json.load(open(p)); d['world']['lastSeen']=old; json.dump(d,open(p,'w'),ensure_ascii=False)
    print("  （已将 lastSeen 回拨 10h）")
else:
    print("  （警告：存档文件未找到，路径="+p+"）")
PY
post /api/idle/finalize "{\"userId\":\"$UID_TEST\"}" \
  | python3 -c "import json,sys; d=json.load(sys.stdin); r=d['result']; print('  时长',r['minutes'],'分 封顶',r['capped'],'经验+',r['exp'],'金币+',r['gold'])"

echo "=== 4) 读档（验证磁盘持久化）==="
post /api/state/load "{\"userId\":\"$UID_TEST\"}" \
  | python3 -c "import json,sys; d=json.load(sys.stdin)['state']; print('  读回 Lv',d['core']['level'],'经验',d['core']['exp'],'金币',d['core']['gold'])"

echo "=== 5) 转职 + 装备换算落库 ==="
post /api/transfer "{\"userId\":\"$UID_TEST\",\"to\":\"warrior\"}" \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print('  职业',d['classId'],d['message'])"

echo "✅ 冒烟测试完成（存档文件：data/saves/$UID_TEST.json）"

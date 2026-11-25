/* ============================================================
   杖剑传说 · 图形界面逻辑（客户端零游戏逻辑）
   ------------------------------------------------------------
   严格单通道导航（不可串界面）：
     [登录页] --进入游戏(唯一入口)--> [大世界枢纽]
     [大世界] --右上4按钮--> [角色|仓库|地图|挂机 面板] --返回(唯一出口)--> [大世界]
     [大世界] --遇怪/交战--> [战斗] --结算--> [大世界]
     [任意处] --退出登录(右下角)--> [登录页]
   守卫规则：
     - 面板只能从 world 打开；面板内不允许打开其他面板（必须返回 world 再进）
     - 战斗/面板期间右上4按钮不可用
     - 弹窗必须经明确按钮关闭，关闭后回到来处
   所有数值来自服务端权威 API，前端只做表现。
   ============================================================ */
(() => {
'use strict';
const RES='/res/';
const API = location.protocol==='file:' ? 'http://127.0.0.1:8080' : '';
const $ = id => document.getElementById(id);
const sleep = ms => new Promise(r=>setTimeout(r,ms));

/* ---------- 全局状态 ---------- */
let CFG=null, state=null;
let view='login';          // login | world | battle （唯一的顶层视图）
let panel=null;            // null | role | home | dungeon | guild | map （只能挂在 world 上）
let homeSub=null;          // 家园二级：null | warehouse | idle
let roleTab='info';        // 角色面板内 tab：info | equip | skill | class
let equipDetailSlot=null;  // 仓库内展开的装备详情部位
let loggedUser=null;       // 当前登录账号（用户名）
let authToken=null;        // 服务端签发的 token（跨设备续玩凭据）
let authUserId=null;       // 服务端 userId
let deviceId=null;         // 本机设备号
let currentMonster=null, currentReport=null;
let battlePlaying=false, paused=false;
let heroStats=null;        // 服务端权威属性（/api/stats/calc），前端不自行推算
let ws=null, clientId=null, onlineCount=1;
let wsChannel=0, wsRetry=0, wsTimers=[];   // 分线号 + 断线退避重连（1/2/4/8s）

/* ---------- 账号体系（服务端 Nakama 语义，token 持久化跨设备续玩） ---------- */
const SESSION_KEY='zjcs_session_v2', DEVICE_KEY='zjcs_device_id';
function getDeviceId(){
  if(deviceId)return deviceId;
  try{ deviceId=localStorage.getItem(DEVICE_KEY); }catch(e){}
  if(!deviceId){ deviceId='dev_'+Date.now().toString(36)+'_'+Math.random().toString(36).slice(2,8); try{localStorage.setItem(DEVICE_KEY,deviceId);}catch(e){} }
  return deviceId;
}
function restoreSession(){
  try{
    const s=JSON.parse(localStorage.getItem(SESSION_KEY)||'null');
    if(s && s.token && s.userId){ authToken=s.token; authUserId=s.userId; loggedUser=s.username||s.userId; return true; }
  }catch(e){}
  authToken=null;authUserId=null;loggedUser=null;return false;
}
function setSession(token,userId,username){
  authToken=token; authUserId=userId; loggedUser=username||userId;
  if(token) try{localStorage.setItem(SESSION_KEY,JSON.stringify({token,userId,username}));}catch(e){}
  else try{localStorage.removeItem(SESSION_KEY);}catch(e){}
}
const uid = () => authUserId || ('web_'+(loggedUser||'hero'));

/* ---------- 网络（自动携带 token；401 时提示重新登录） ---------- */
async function api(path, body, method){
  const headers={'Content-Type':'application/json'};
  if(authToken) headers['Authorization']='Bearer '+authToken;
  const opt = {method: method||(body?'POST':'GET'), headers};
  if(body) opt.body=JSON.stringify(body);
  // 同时在 query 带 token：部分网关会剥离 Authorization 头，服务端 authUserID 支持 ?token= 兜底
  let url=API+path;
  if(authToken) url+=(url.indexOf('?')>=0?'&':'?')+'token='+encodeURIComponent(authToken);
  const r = await fetch(url, opt);
  if(!r.ok){ let msg='HTTP '+r.status; try{const txt=await r.text(); try{const e=JSON.parse(txt);msg=e.error||e.message||msg;}catch(e){ if(txt&&txt.trim())msg=txt.trim(); }}catch(e){} throw new Error(msg); }
  return r.json();
}

/* ---------- 资源预加载 ---------- */
const IMG={};
function loadImg(src){return new Promise(res=>{const im=new Image();im.onload=()=>res(im);im.onerror=()=>res(null);im.src=src;});}
async function preload(){
  // v3.0 部件动画：角色/怪物/宝箱由 PartActor 按需加载，这里只预载 UI 卡/图标/背景
  const jobs=[], add=(p,key)=>jobs.push(loadImg(p).then(im=>{IMG[key]=im;}));
  for(const b of ['grassland','forest','desert','snow','cave']) add(`${RES}v3/backgrounds/bg_explore_${b}.png`,'bg_'+b);
  add(`${RES}v3/backgrounds/bg_battle_arena.png`,'bg_arena');
  for(const c of ['warrior','mage','fighter','knight','sage','warlock']) add(`${RES}v3/characters/cards/ui_card_${c}.png`,'card_'+c);
  for(const ic of ['bag','coin','guild','hero','map','potion','quest','skill','storage','sword']) add(`${RES}v3/icons/icon_${ic}.png`,'icon_'+ic);
  // 预载部件动画 bundle（角色+怪物+宝箱）
  const parts=['char_hero_mage','mon_slime','mon_skeleton','mon_bat','mon_boss_beetle','item_chest_wood','item_chest_silver','item_chest_gold'];
  jobs.push(window.PartActor.preload(parts));
  let done=0; const total=jobs.length;
  await Promise.all(jobs.map(j=>Promise.resolve(j).then(()=>{$('loadBar').style.width=Math.round(++done/total*100)+'%';})));
  $('loadTxt').textContent='就绪';
}

/* ---------- 视口缩放 ---------- */
function fitStage(){
  const s=Math.min(innerWidth/1280, innerHeight/720);
  $('stage').style.transform=`translate(-50%,-50%) scale(${s})`;
}
addEventListener('resize',fitStage); fitStage();

/* ============================================================
   Canvas 渲染
   ============================================================ */
const cvb=$('cvBattle').getContext('2d');
cvb.imageSmoothingEnabled=false;

/* 俯视大地图引擎：回调注入到本模块的状态机 */
const worldMap=new WorldMap($('cvExplore'),{
  onEncounter:(mon)=>askEncounter(mon),          // 点怪物 → 交战确认
  onChest:(chest)=>onChestOpened(chest),         // 开箱动画开始 → 服务端权威入账
  onGate:(gate)=>askTravel(gate),                // 点传送门 → 世界切换确认
  onHint:(msg)=>{ const el=$('worldHintText'); if(el){ el.textContent=msg; clearTimeout(el._t); el._t=setTimeout(()=>{el.textContent='WASD/方向键移动 · 点怪交战 · 点宝箱开启 · 点⇄传送';},2200);} },
  onTile:(k)=>{ /* 记录探索格子 */ }
});
window.__wm=worldMap; // 调试钩子：便于自动化读取实体坐标（不影响玩法）
const miniCtx=$('miniMapCv').getContext('2d');

// 小地图：真实雷达（以角色为中心，与通行掩码同源，北向上）
// 旧版是写死的装饰底图（假水域/假十字路），与实际路网无关 → 位置"对不上"
const MM_S=180, MM_RANGE=2048;              // 显示直径 2048 世界单位（约 1.6 屏）
const MM_K=MM_S/MM_RANGE;                   // 世界单位 → 小地图像素
function drawMiniMap(){
  if(!worldMap.ready)return;
  const g=miniCtx, S=MM_S, C=S/2, R=C-2;
  const hx=worldMap.hero.x, hy=worldMap.hero.y;
  g.clearRect(0,0,S,S);
  g.save();
  g.beginPath();g.arc(C,C,R,0,Math.PI*2);g.clip();
  g.fillStyle='#0f1a12';g.fillRect(0,0,S,S);  // 世界外底色
  // ---- 底图：优先真实地形缩略（与主画面同一张图），失败用掩码上色兜底 ----
  const MC=WorldMapConst||{};
  let img=null,SC=0,IW=0,IH=0;
  if(worldMap.thumb&&MC.THUMB_W){img=worldMap.thumb;SC=MC.THUMB_SCALE;IW=MC.THUMB_W;IH=MC.THUMB_H;}
  else if(worldMap.miniCv&&MC.MINI_W){img=worldMap.miniCv;SC=MC.MINI_SCALE;IW=MC.MINI_W;IH=MC.MINI_H;}
  if(img){
    const span=MM_RANGE/SC, k=S/span;        // 源 span px → 目标 180px
    let sx=hx/SC-span/2, sy=hy/SC-span/2, sw=span, sh=span, dx=0, dy=0;
    if(sx<0){dx=-sx*k;sw+=sx;sx=0;}
    if(sy<0){dy=-sy*k;sh+=sy;sy=0;}
    if(sx+sw>IW)sw=IW-sx;
    if(sy+sh>IH)sh=IH-sy;
    if(sw>0&&sh>0)g.drawImage(img,sx,sy,sw,sh,dx,dy,sw*k,sh*k);
  }
  // ---- 实体点位：真实坐标 → 雷达坐标 ----
  const mx=x=>C+(x-hx)*MM_K, my=y=>C+(y-hy)*MM_K;
  const R2=(R-3)*(R-3);
  const inside=(px,py)=>{const a=px-C,b=py-C;return a*a+b*b<=R2;};
  const dot=(x,y,color,r)=>{const px=mx(x),py=my(y);if(!inside(px,py))return;g.fillStyle=color;g.beginPath();g.arc(px,py,r||3,0,Math.PI*2);g.fill();};
  // 传送门（蓝）
  for(const gt of (worldMap.gates||[]))dot(gt.x,gt.y,'#6fc8ff',3.5);
  // 怪物（红）
  for(const m of worldMap.monsters)if(m.state==='idle')dot(m.x,m.y,'#ff5d5d',3);
  // 宝箱（金）
  for(const c of worldMap.chests)dot(c.x,c.y,'#f0d590',3);
  // 其他玩家（青）
  for(const k2 in (worldMap.remote||{})){const r=worldMap.remote[k2];if(r)dot(r.x,r.y,'#8fd8ff',3.5);}
  // ---- 角色：圆心绿点 + 朝向三角 ----
  g.fillStyle='#7fe0a0';g.beginPath();g.arc(C,C,5.5,0,Math.PI*2);g.fill();
  g.strokeStyle='#fff';g.lineWidth=1.6;g.stroke();
  const dir=worldMap.hero.facing||worldMap.hero.dir||'down';
  const ang={up:-Math.PI/2,down:Math.PI/2,left:Math.PI,right:0}[dir]||Math.PI/2;
  g.save();g.translate(C,C);g.rotate(ang);
  g.fillStyle='#eafff3';g.beginPath();g.moveTo(10,0);g.lineTo(4,-4);g.lineTo(4,4);g.closePath();g.fill();
  g.restore();
  g.restore();
  // ---- 外圈（金环 + 内细环），圆外为方向标 ----
  g.strokeStyle='rgba(217,180,91,.35)';g.lineWidth=1;
  g.beginPath();g.arc(C,C,R-4,0,Math.PI*2);g.stroke();
  g.strokeStyle='#d9b45b';g.lineWidth=3;
  g.beginPath();g.arc(C,C,R,0,Math.PI*2);g.stroke();
  $('mmProgress').textContent=(worldMap.progress||0)+'/20';
}

let bAnim={hero:'idle',mon:'idle',heroUntil:0,monUntil:0};
let bHp={hero:640,heroMax:640,mon:420,monMax:420};
let bFx=[];
let battleHeroActor=null, battleMonActor=null, battleMonBundle='';
function setBAnim(who,s,dur){
  bAnim[who]=s;bAnim[who+'Until']=performance.now()+(dur||0);
  if(who==='hero'&&battleHeroActor)battleHeroActor.play(s);
  if(who==='mon'&&battleMonActor)battleMonActor.play(s);
}
function curBAnim(who){return performance.now()<(bAnim[who+'Until']||0)?bAnim[who]:'idle';}
function spawnFx(text,x,y,color){bFx.push({text:String(text),x,y,color:color||'#ff6b6b',born:performance.now()});}
// 准备战斗部件 actor（英雄固定法师，怪物按类型）
function ensureBattleActors(){
  if(!battleHeroActor){battleHeroActor=new window.PartActor('char_hero_mage');battleHeroActor.init().then(()=>battleHeroActor.play('idle'));}
  const sp=currentMonster?(currentMonster.sprite||currentMonster.type||'slime'):'slime';
  const bundle=window.MON_BUNDLE&&window.MON_BUNDLE[sp]?window.MON_BUNDLE[sp]:'mon_'+sp;
  if(bundle!==battleMonBundle||!battleMonActor){
    battleMonBundle=bundle;
    battleMonActor=new window.PartActor(bundle);
    battleMonActor.init().then(()=>battleMonActor.play('idle'));
  }
}
function drawBattle(t,now){
  const bg=IMG['bg_arena'];
  if(bg) cvb.drawImage(bg,0,0,1280,720); else {cvb.fillStyle='#26202a';cvb.fillRect(0,0,1280,720);}
  ensureBattleActors();
  // 若动画到期，回 idle
  if(curBAnim('hero')==='idle'&&battleHeroActor&&battleHeroActor.state!=='idle')battleHeroActor.play('idle');
  if(curBAnim('mon')==='idle'&&battleMonActor&&battleMonActor.state!=='idle')battleMonActor.play('idle');
  cvb.fillStyle='rgba(0,0,0,.35)';
  cvb.beginPath();cvb.ellipse(430,560,52,14,0,0,7);cvb.fill();
  cvb.beginPath();cvb.ellipse(870,560,58,15,0,0,7);cvb.fill();
  if(battleHeroActor)battleHeroActor.render(cvb,430,555,3,{});
  if(battleMonActor)battleMonActor.render(cvb,870,558,currentMonster&&currentMonster.type==='boss_beetle'?3.6:3,{dir:-1});
  // 程序化飘字（v3.0 飘字由代码渲染，不依赖 fx PNG）
  bFx=bFx.filter(f=>now-f.born<620);
  for(const f of bFx){
    const age=(now-f.born)/620;
    cvb.save();cvb.globalAlpha=1-age;
    cvb.font='bold 30px sans-serif';cvb.textAlign='center';
    cvb.strokeStyle='#000';cvb.lineWidth=5;cvb.strokeText(f.text,f.x,f.y-50*age);
    cvb.fillStyle=f.color||'#ff6b6b';cvb.fillText(f.text,f.x,f.y-50*age);
    cvb.restore();
  }
}
function renderLoop(now){
  const t=now/1000;
  perfTick(now);
  if(view==='world'){
    worldMap.render(now);
    // 小地图/交互提示降频 10Hz：避免每帧重绘小画布 + DOM 写入造成卡顿
    if(now-(renderLoop._auxTs||0)>100){
      renderLoop._auxTs=now;
      drawMiniMap();
      updateInteractHint();
    }
  }
  else if(view==='battle') drawBattle(t,now);
  requestAnimationFrame(renderLoop);
}
/* 性能角标：版本 + FPS + 本机 GPU 纹理上限（判断超大地图是否触碰硬件上限） */
const BUILD_VER='v1.7.5';
let _perfFrames=0,_perfTs=0,_perfFps=0,_texMax='?';
function perfTick(now){
  _perfFrames++;
  if(now-_perfTs>=1000){
    _perfFps=Math.round(_perfFrames*1000/(now-_perfTs));
    _perfFrames=0;_perfTs=now;
    if(_texMax==='?'){
      try{
        const gl=document.createElement('canvas').getContext('webgl');
        _texMax=gl?String(gl.getParameter(gl.MAX_TEXTURE_SIZE)):'无WebGL';
      }catch(e){_texMax='无WebGL';}
    }
    const el=$('perfBadge');
    if(el)el.textContent=`${BUILD_VER} · ${_perfFps} FPS · 纹理上限 ${_texMax}`;
  }
}
// 靠近实体时的交互提示
function updateInteractHint(){
  const h=worldMap.hero, el=$('interactHint');
  if(!h||!worldMap.ready){el.classList.add('hidden');return;}
  const mon=worldMap.nearestMonster(h.x,h.y,90);
  const ch=worldMap.nearestChest(h.x,h.y,80);
  if(mon){el.textContent='⚔ 点击「'+mon.name+'」交战';el.classList.remove('hidden');}
  else if(ch){el.textContent='📦 点击宝箱开启（'+({wood:'木',silver:'银',gold:'金'}[ch.tier])+'）';el.classList.remove('hidden');}
  else el.classList.add('hidden');
}

/* ============================================================
   ★ 单通道导航状态机（核心）
   ============================================================ */
function hideModal(){ $('modalLayer').classList.add('hidden'); }
function showModal(html){ $('modalBox').innerHTML=html; $('modalLayer').classList.remove('hidden'); }

// 切换到顶层视图：离开 world 时强制收起所有面板与遇怪弹窗
function showView(v){
  view=v;
  $('loginView').classList.toggle('hidden', v!=='login');
  $('worldView').classList.toggle('hidden', v!=='world');
  $('battleView').classList.toggle('hidden', v!=='battle');
  if(v!=='world'){
    panel=null; equipDetailSlot=null;
    $('panelLayer').classList.add('hidden');
    $('interactHint').classList.add('hidden');
    hideModal();
  }
  // 地图激活：仅大世界态（面板打开时通过 active 再关闭，防操作冲突）
  worldMap.active=(v==='world'&&!panel);
  // 退出登录按钮：登录后始终可见（唯一出口）
  $('logoutBtn').classList.toggle('hidden', !loggedUser);
}

/* ---- 登录页 ---- */
function refreshLoginView(){
  const ok=!!loggedUser;
  $('authBox').classList.toggle('hidden', ok);
  $('enterBox').classList.toggle('hidden', !ok);
  $('welcomeName').textContent=loggedUser||'';
  $('authErr').textContent='';
  $('logoutBtn').classList.toggle('hidden', !ok);
}
async function doRegister(){
  const u=$('authUser').value.trim(), p=$('authPwd').value;
  if(!u||!p){ $('authErr').textContent='请填写账号和密码'; return; }
  try{
    const r=await api('/api/auth/register',{username:u,password:p,deviceId:getDeviceId()});
    setSession(r.token,r.userId,r.username);
    refreshLoginView();
  }catch(e){ $('authErr').textContent=e.message; }
}
async function doLogin(){
  const u=$('authUser').value.trim(), p=$('authPwd').value;
  if(!u||!p){ $('authErr').textContent='请填写账号和密码'; return; }
  try{
    const r=await api('/api/auth/login',{username:u,password:p,deviceId:getDeviceId()});
    setSession(r.token,r.userId,r.username);
    refreshLoginView();
  }catch(e){ $('authErr').textContent=e.message; }
}
async function doDeviceLogin(){
  try{
    const r=await api('/api/auth/device',{deviceId:getDeviceId()});
    setSession(r.token,r.userId,r.username||('冒险者'+r.userId.slice(-4)));
    refreshLoginView();
  }catch(e){ $('authErr').textContent=e.message; }
}
function switchAccount(){ setSession(null); $('authUser').value=''; $('authPwd').value=''; state=null; closeWS(); refreshLoginView(); }

// 唯一入口：登录页 → 大世界
async function enterGame(){
  if(!loggedUser){ showView('login'); return; }
  try{
    const r=await api('/api/state/load',{userId:uid()});
    if(r.exists){ state=r.state; }
    else { state=await api('/api/character/new',{userId:uid(),name:loggedUser,classId:'mage'}); }
    try{
      heroStats=await api('/api/stats/calc',{avatar:{level:state.core.level,classId:state.core.classId},items:state.equip||[]});
    }catch(e){ heroStats=null; }
    syncHud();
    // 初始化地图引擎（首次）
    if(!worldMap.ready){
      $('loadTxt').textContent='正在生成大世界…';
      worldMap.opts.worldDefs=(CFG&&CFG.worlds&&CFG.worlds.worlds)||[];
      await worldMap.load();
    }
    // 进入玩家当前世界并拉取服务端点位
    await loadWorld(state.world.worldId||'W1', state.world.x, state.world.y);
    showView('world');
    connectWS();
  }catch(e){ $('loadTxt').textContent='进入失败：'+e.message; $('loading').classList.remove('hidden'); }
}
// 加载世界：切主题地图 + 拉取 ACTIVE 点位
async function loadWorld(worldId, heroX, heroY){
  const spots=await api('/api/spawn?worldId='+encodeURIComponent(worldId)+'&userId='+encodeURIComponent(uid()));
  await worldMap.switchWorld(worldId, spots.spots, heroX, heroY);
  const wd=(CFG.worlds.worlds||[]).find(w=>w.id===worldId);
  $('worldTitle').textContent=worldId+' · '+(wd?wd.name:'大世界');
  $('worldSceneName').textContent=(wd?wd.name:'大世界')+' · 大世界';
  syncHud();
}
// 点传送门 → 确认弹窗（单通道）
function askTravel(gate){
  if(battlePlaying||panel||view!=='world')return;
  worldMap.active=false;
  const req=gate.requireLevel||0;
  const lv=state?state.core.level:1;
  const locked=req>0 && lv<req;
  showModal(`<h2>世界通道</h2>
    <div class="msub">${gate.label}<br>当前等级 Lv.${lv} ${req>0?'· 需 Lv.'+req:''}${locked?'<br><span style="color:#ffcf7a">等级不足，请先打怪或挂机升至 Lv.'+req+'</span>':''}</div>
    <div class="divider"></div>
    <div class="enc-btns"><button class="btn-red" id="goTravel" ${locked?'disabled style="opacity:.45;cursor:not-allowed"':''}>${locked?'等级不足':'传送'}</button><button class="btn-gray" id="cancelTravel">留在原地</button></div>`);
  if(locked){ $('goTravel').onclick=null; }
  else { $('goTravel').onclick=()=>{hideModal();travelTo(gate);}; }
  $('cancelTravel').onclick=()=>{hideModal();worldMap.active=true;};
}
// 同端口 WebSocket：多人位置同步（断线退避重连 1/2/4/8s + 服务端快照恢复）
function connectWS(){
  closeWS(true); // 主动清理旧定时器，但不重置退避计数
  const proto=location.protocol==='https:'?'wss:':'ws:';
  clientId=authUserId||('c_'+Date.now());
  const url=`${proto}//${location.host}/ws?token=${encodeURIComponent(authToken||'')}&clientId=${encodeURIComponent(clientId)}`;
  try{
    ws=new WebSocket(url);
    ws.onopen=()=>{
      wsRetry=0; // 连接成功重置退避
      // 带旧 channel 重连，服务端未满则复用原线
      ws.send(JSON.stringify({type:'match_join',worldId:worldMap.worldId,channel:wsChannel,name:loggedUser}));
      $('wsHint').textContent='多人已连接'+(wsChannel>0?'·'+wsChannel+'线':'');
      wsTimers.push(setInterval(()=>{if(ws&&ws.readyState===1)ws.send(JSON.stringify({type:'heartbeat'}));},5000));
      // 定期上报位置（10Hz）
      wsTimers.push(setInterval(()=>{
        if(ws&&ws.readyState===1){
          const h=worldMap.hero;
          ws.send(JSON.stringify({type:'player_move',x:Math.round(h.x),y:Math.round(h.y),seq:Date.now()}));
        }
      },100));
    };
    ws.onmessage=e=>{
      let d;try{d=JSON.parse(e.data);}catch(err){return;}
      if(d.type==='match_joined'){
        wsChannel=d.channel||0;
        $('wsHint').textContent='多人已连接'+(wsChannel>0?'·'+wsChannel+'线':'');
      }else if(d.type==='state_snapshot'){
        worldMap.applySnapshot(d.players||[],clientId); // 视野裁剪在 worldmap 内做
        onlineCount=(d.players||[]).length;
        $('onlineBadge').textContent='● 在线 '+onlineCount;
      }
    };
    ws.onclose=()=>{
      $('wsHint').textContent='连接断开·重连中';
      scheduleReconnect();
    };
    ws.onerror=()=>{ $('wsHint').textContent='连接异常·重连中'; };
  }catch(e){ $('wsHint').textContent='单人模式'; scheduleReconnect(); }
}
// 退避重连：1s→2s→4s→8s，上限 8s
function scheduleReconnect(){
  if(!authToken && !authUserId) return; // 未登录不重连
  const backoff=[1000,2000,4000,8000];
  const delay=backoff[Math.min(wsRetry,backoff.length-1)];
  wsRetry++;
  clearTimeout(wsReconnectTimer);
  wsReconnectTimer=setTimeout(()=>{ if(worldViewActive()) connectWS(); },delay);
}
let wsReconnectTimer=null;
function worldViewActive(){ const v=document.querySelector('.view:not(.hidden)'); return !v||v.id==='worldView'; }
// closeWS(keepRetry)：退出登录/切账号时彻底停止；重连建连时只清定时器保留退避
function closeWS(keepRetry){
  wsTimers.forEach(t=>clearInterval(t)); wsTimers=[];
  if(ws){try{ws.onclose=null;ws.close();}catch(e){}ws=null;}
  if(!keepRetry){ wsRetry=0; clearTimeout(wsReconnectTimer); wsChannel=0; }
}

// 切换世界（走 GATE，服务端校验门槛 + 落库）
async function travelTo(gate){
  if(battlePlaying||panel)return;
  try{
    const r=await api('/api/world/travel',{to:gate.to,userId:uid()});
    if(r.state){ state=r.state; }
    $('modalLayer').classList.add('hidden');
    await loadWorld(gate.to, state.world.x, state.world.y);
    toastModal('传送成功','已抵达 '+$('worldTitle').textContent);
    if(ws&&ws.readyState===1)ws.send(JSON.stringify({type:'match_join',worldId:gate.to,name:loggedUser}));
  }catch(e){ toastModal('传送失败',e.message); worldMap.active=true; }
}
// 唯一出口：任意处 → 登录页
function doLogout(){
  setSession(null); state=null; panel=null; equipDetailSlot=null;
  closeWS(); worldMap.active=false;
  showView('login'); refreshLoginView();
}

// 打开面板：只能从 world，且当前无任何面板/弹窗/遇怪
function openPanel(key){
  if(view!=='world') return;                                   // 战斗/登录态禁止
  if(panel) return;                                             // 禁止面板互串
  if(!$('modalLayer').classList.contains('hidden')) return;     // 弹窗中禁止
  if(battlePlaying) return;
  panel=key; equipDetailSlot=null; homeSub=null;
  worldMap.active=false;                                        // 面板期间冻结地图
  const meta={role:'角色',home:'家园',dungeon:'副本',guild:'公会',map:'世界地图',warehouse:'仓库',idle:'挂机设置'};
  $('panelTitle').textContent=meta[key]||'';
  $('panelSub').textContent='';
  renderPanel();
  $('panelLayer').classList.remove('hidden');
}
// 面板唯一出口：返回大世界
function closePanel(){
  if(view!=='world') return;
  panel=null; equipDetailSlot=null; homeSub=null;
  $('panelLayer').classList.add('hidden');
  worldMap.active=true;                                         // 恢复地图
}

/* ---- HUD 同步（大世界左上信息） ---- */
function fmt(n){return Number(n||0).toLocaleString('zh-CN');}
function syncHud(){
  if(!state||!state.core)return;
  const c=state.core;
  const cdef=CFG&&CFG.classes.classes.find(x=>x.id===c.classId);
  const cname=cdef?cdef.name:c.classId;
  $('worldName').textContent=cname+' · '+(c.name||'英雄');
  const power=heroStats&&heroStats.power?heroStats.power:c.battlePower;
  $('worldLv').textContent=`Lv.${c.level} · 战力 ${fmt(power)} · 💰${fmt(c.gold)} 💎${fmt(c.gem)}`;
  const iconKey=(cdef&&cdef.icon)?cdef.icon.replace('ui_card_','').replace('.png',''):'warrior';
  $('worldClsIco').src=RES+'characters/cards/ui_card_'+iconKey+'.png';
  $('heroName').textContent=(c.name||'英雄')+' Lv.'+c.level;
}
async function refreshStats(){
  if(!state||!state.core)return;
  try{
    heroStats=await api('/api/stats/calc',{avatar:{level:state.core.level,classId:state.core.classId},items:state.equip||[]});
  }catch(e){}
}
function attrRows(){
  const s=heroStats||{};
  const rows=[
    ['攻击', Number(s.atk||0).toFixed(1)],
    ['防御', Number(s.def||0).toFixed(1)],
    ['生命', Math.round(s.hp||0)],
    ['暴击', (Number(s.crit||0)*100).toFixed(0)+'%'],
    ['速度', Math.round(s.spd||10)],
  ];
  return rows.map(([k,v])=>`<div class="attr-row"><span class="k">${k}</span><span class="v">${v}</span></div>`).join('');
}

/* ============================================================
   面板渲染（单入口单出口，面板内 tab 不跨面板）
   ============================================================ */
function renderPanel(){
  if(panel==='role') return renderRole();
  if(panel==='home') return renderHome();
  if(panel==='dungeon') return renderDungeon();
  if(panel==='guild') return renderGuild();
  if(panel==='map') return renderMap();
  // 兼容旧入口：仓库/挂机作为家园二级页
  if(panel==='warehouse') return renderWarehouse();
  if(panel==='idle') return renderIdle();
}
function tabRow(active){
  const tabs=[['info','角色'],['equip','装备栏'],['skill','技能'],['class','转职']];
  return `<div class="tab-row">${tabs.map(([k,n])=>`<button class="tab ${k===active?'on':''}" data-rtab="${k}">${n}</button>`).join('')}</div>`;
}

/* ---- 家园页（v1.6）：聚合 仓库 + 挂机 两个二级入口 ---- */
function renderHome(){
  $('panelTitle').textContent='家园';
  $('panelSub').textContent='家园装饰与功能入口 · 点击卡片进入对应子页';
  const body=$('panelBody');
  if(homeSub==='warehouse') return renderWarehouse(true);
  if(homeSub==='idle') return renderIdle(true);
  const used=(state?.warehouse?.length)||12;
  const homeInfo=state?.home||{cart:0,bed:0};
  body.innerHTML=`
    <div class="home-scene" style="position:relative;height:100px;border-radius:10px;margin-bottom:12px;background:linear-gradient(160deg,#1c2a3a,#0d1420);border:1px solid #2a3a52;overflow:hidden;display:flex;align-items:flex-end;justify-content:center;gap:20px;padding-bottom:8px">
      <div class="deco-item" data-deco="cart" style="text-align:center;cursor:pointer"><div style="font-size:34px">🛒</div><div style="font-size:11px;color:#c9a227">手推车 Lv.${homeInfo.cart||0}</div></div>
      <div class="deco-item" data-deco="bed" style="text-align:center;cursor:pointer"><div style="font-size:34px">🛏️</div><div style="font-size:11px;color:#8fd0ff">营地床 Lv.${homeInfo.bed||0}</div></div>
    </div>
    <div class="home-cards">
      <button class="home-card hc-warehouse" id="hcWarehouse">
        <span class="hc-ico">🎒</span><span class="hc-title">仓库</span>
        <span class="hc-desc">物品存放与整理</span><span class="hc-enter">进入</span>
      </button>
      <button class="home-card hc-idle" id="hcIdle">
        <span class="hc-ico">⏳</span><span class="hc-title">挂机收益</span>
        <span class="hc-desc">离线领取与结算</span><span class="hc-enter">进入</span>
      </button>
    </div>
    <div class="home-note">点击卡片进入子页 · 点击场景装饰可升级</div>
    <div class="home-status">仓库：容量 ${used}/200 格　｜　挂机：已结算 8h 上限</div>`;
  $('hcWarehouse').onclick=()=>{homeSub='warehouse';renderHome();};
  $('hcIdle').onclick=()=>{homeSub='idle';renderHome();};
  body.querySelectorAll('.deco-item').forEach(el=>el.onclick=async()=>{
    try{
      const r=await api('/api/player/action',{userId:uid(),action:'home_decorate',itemId:el.dataset.deco});
      if(r.state){state=r.state;syncHud();}
      toastModal('装饰升级',el.dataset.deco+' 等级提升');renderHome();
    }catch(e){toastModal('升级失败',e.message);}
  });
}

/* ---- 副本页（v1.6）：副本列表 + 进入战斗 ---- */
const DUNGEONS=[
  {id:'grass',name:'青草试炼',lv:1,perDay:3,drop:'掉落基础材料',mon:'slime',elite:false},
  {id:'mist', name:'迷雾回廊',lv:10,perDay:2,drop:'掉落强化材料',mon:'bat',elite:false},
  {id:'flame',name:'赤焰深渊',lv:20,perDay:2,drop:'掉落装备',mon:'skeleton',elite:true},
  {id:'frost',name:'霜冻之巅',lv:30,perDay:1,drop:'掉落稀有材料',mon:'boss_beetle',elite:true},
];
function renderDungeon(){
  $('panelTitle').textContent='副本';
  $('panelSub').textContent='选择副本进入独立战斗场景（每日次数服务端权威）';
  const body=$('panelBody');
  const lv=state&&state.core?state.core.level:1;
  const dungeons=(CFG?.economy?.dungeons)||DUNGEONS;
  // 拉取各副本今日剩余次数（服务端权威）
  Promise.all(dungeons.map(d=>api('/api/player/action',{userId:uid(),action:'dungeon_remain',dungeonId:d.id}).catch(()=>({remain:d.perDay})))).then(rems=>{
    if(!body.isConnected)return; // 面板已切换，避免残留渲染
    dungeons.forEach((d,i)=>d._remain=(rems[i]?.remain!=null?rems[i].remain:d.perDay));
    body.innerHTML=`
      <div class="dungeon-list">${dungeons.map(d=>{
        const locked=lv<d.lv, exhausted=(d._remain!=null?d._remain:d.perDay)<=0;
        const dis=locked||exhausted;
        return `<div class="dungeon-item">
          <div class="di-info"><div class="di-name">${d.name}</div>
            <div class="di-meta">Lv${d.lv}+ · 今日剩余 ${d._remain!=null?d._remain:d.perDay}/${d.perDay} · ${d.drop}</div></div>
          <button class="di-enter" data-dg="${d.id}" ${dis?'disabled style="opacity:.45;cursor:not-allowed"':''}>${locked?'等级不足':exhausted?'次数用完':'进入'}</button>
        </div>`;
      }).join('')}</div>
      <div class="home-note">副本为独立战斗场景，每日次数服务端权威计数、0 点重置，结算后返回副本列表</div>`;
    body.querySelectorAll('.di-enter').forEach(btn=>{
      if(btn.disabled)return;
      btn.onclick=async()=>{
        const d=dungeons.find(x=>x.id===btn.dataset.dg);
        if(!d)return;
        try{
          // 服务端权威校验等级+扣次数
          const r=await api('/api/player/action',{userId:uid(),action:'dungeon_enter',dungeonId:d.id});
          if(r.state){state=r.state;syncHud();}
          hideModal();
          closePanel();
          const mon={x:0,y:0,type:r.monsterId||d.mon,monId:r.monsterId||d.mon,name:d.name,elite:r.elite!=null?r.elite:d.elite,state:'idle',nodeId:'dungeon_'+d.id};
          doFight(mon);
        }catch(e){ toastModal('无法进入',e.message); }
      };
    });
  });
}

/* ---- 公会占位页（v1.6）：本期 Out of Scope ---- */
function renderGuild(){
  $('panelTitle').textContent='公会';
  $('panelSub').textContent='';
  const body=$('panelBody');
  body.innerHTML=`
    <div class="guild-placeholder">
      <h2>公 会</h2>
      <p>公会系统开发中 · 敬请期待</p>
      <span class="gp-sub">本期原型不包含公会功能（Out of Scope）</span>
      <div class="gp-note">入口保留，功能待后续版本实现</div>
    </div>`;
}
function renderRole(){
  const body=$('panelBody');
  if(!state||!state.core){body.innerHTML='<div class="sec-sub">存档未就绪</div>';return;}
  const c=state.core;
  const cdef=CFG.classes.classes.find(x=>x.id===c.classId);
  let html=tabRow(roleTab);
  if(roleTab==='info'){
    const eq=state.equip||[];
    const done=eq.filter(e=>e.equipped).length;
    html+=`
      <div class="sec-title">${cdef?cdef.name:c.classId} · ${c.name||'英雄'}</div>
      <div class="sec-sub">Lv.${c.level} ｜ 战力 ${fmt(c.battlePower)} ｜ 已装备 ${done} 件</div>
      <div style="text-align:center;margin:8px 0 14px"><img src="${RES}v3/characters/cards/${(cdef&&cdef.icon)||'ui_card_mage.png'}" style="width:130px;height:170px;object-fit:contain;image-rendering:pixelated;filter:drop-shadow(0 8px 18px rgba(0,0,0,.6))"></div>
      <div class="sec-title">基础属性</div>${attrRows()}`;
  } else if(roleTab==='equip'){
    const eq=state.equip||[];
    const slotNames={weapon:'武器',helmet:'头盔',armor:'护甲',trinket:'饰品'};
    const slotIco={weapon:'🗡️',helmet:'🪖',armor:'🛡️',trinket:'💍'};
    const order=['weapon','helmet','armor','trinket'];
    const cells=order.map(id=>{
      const e=eq.find(x=>x.slot===id&&x.equipped);
      return `<div class="equip-cell ${e?'worn':''}"><div class="ei">${slotIco[id]}</div><div class="en">${slotNames[id]}</div><div class="el">${e?e.id+' +'+e.enhance:'空'}</div></div>`;
    }).join('');
    const rows=eq.map(e=>`
      <div class="preview-box" style="display:flex;align-items:center;gap:8px">
        <div style="flex:1"><div class="pt">${e.id} <span style="font-size:11px;color:${e.equipped?'#7fe0a0':'#9aa7bd'}">${e.equipped?'· 已穿戴':'· 仓库中'}</span></div>
          <div class="sec-sub">${slotNames[e.slot]||e.slot} ｜ 强化 +${e.enhance||0}</div></div>
        <button class="btn-gray eq-toggle" data-eqid="${e.id}" data-slot="${e.slot}">${e.equipped?'卸下':'穿戴'}</button>
        ${e.equipped?`<button class="btn-gray eq-enh" data-slot="${e.slot}">强化</button>`:''}
      </div>`).join('');
    html+=`
      <div class="sec-sub">已穿戴 4 槽，点击下方按钮拆装/强化（服务端权威，战力即时更新）</div>
      <div class="equip-grid">${cells}</div>
      <div class="sec-title">装备列表</div>${rows||'<div class="sec-sub">暂无装备</div>'}
      <div class="sec-title">当前属性</div>${attrRows()}`;
  } else if(roleTab==='skill'){
    // 真 BD 配置：本地暂存槽位，提交服务端权威校验（解锁/互斥/4槽）
    if(!window._bdDraft) window._bdDraft=[...(state.skills?.slots||[])];
    const equipped=window._bdDraft;
    const all=CFG.skills.skills;
    const unlocked=state.skills?.unlocked||['slash'];
    const color=['sk-fire','sk-ice','sk-heal','sk-shield'];
    const kindDesc=s=>s.kind==='heal'?'恢复生命':s.kind==='shield'?'吸收伤害':s.kind==='buff'?'增益':s.kind==='debuff'?'减益':'单体伤害';
    const cards=equipped.map((sid,i)=>{
      const s=all.find(x=>x.id===sid)||all[0];
      return `<div class="skill-card ${color[i%4]}" data-slot="${i}" style="cursor:pointer"><div><div class="sn">${s.name}</div><div class="sd">${kindDesc(s)}</div></div><div class="srm" style="font-size:10px;color:#ffb">点击卸下</div></div>`;
    }).join('');
    const lib=all.filter(s=>!equipped.includes(s.id)).map(s=>{
      const lock=!unlocked.includes(s.id);
      return `<div class="lib-item ${lock?'locked':''}" data-skill="${s.id}" style="cursor:${lock?'not-allowed':'pointer'};opacity:${lock?'.45':'1'}">${s.name}${lock?' 🔒':''}</div>`;
    }).join('');
    html+=`
      <div class="sec-sub">战前 BD：点击技能库装配（蓝=已解锁，灰=未解锁），点击槽位卸下。同流派技能互斥，最多 4 槽。</div>
      <div class="skill-grid2">${cards}</div>
      <div class="sec-title">技能库（点击解锁/装配）</div><div class="lib-grid">${lib}</div>
      <button class="big-btn goldbtn" id="submitBD" style="margin-top:12px">保存 BD 配置</button>
      <div class="sec-sub" style="margin-top:8px">已解锁 ${unlocked.length}/${all.length} · 未解锁技能点击花费 300 钻石解锁</div>`;
  } else if(roleTab==='class'){
    const cards=CFG.classes.classes.map(cl=>{
      const sel=cl.id===c.classId;
      const col={'warrior':'#b8862f','mage':'#7C6BB8','fighter':'#c05252','knight':'#3f7fb8','sage':'#3f9d6d','warlock':'#8a4a9c'}[cl.id]||'#5a6b80';
      return `<div class="class-card ${sel?'sel':''}" style="background:linear-gradient(150deg,${col},rgba(0,0,0,.4))" data-cls="${cl.id}"><div><div class="cn">${cl.name}</div><div class="cd">${cl.desc||'切换后装备属性自动换算'}</div></div><span class="chk">${sel?'✓':''}</span></div>`;
    }).join('');
    html+=`
      <div class="sec-sub">当前：${cdef?cdef.name:c.classId} Lv.${c.level} ｜ 转职零成本，装备自动换算</div>
      ${cards}
      <button class="big-btn" id="doTransfer">确认转职</button>`;
  }
  body.innerHTML=html;
  body.querySelectorAll('[data-rtab]').forEach(el=>el.onclick=()=>{roleTab=el.dataset.rtab;window._bdDraft=null;renderRole();});
  if(roleTab==='skill'){
    // 装配：点技能库
    body.querySelectorAll('.lib-item[data-skill]').forEach(el=>el.onclick=async()=>{
      const sid=el.dataset.skill;
      const unlocked=state.skills?.unlocked||[];
      if(!unlocked.includes(sid)){
        try{
          const r=await api('/api/player/action',{userId:uid(),action:'unlock_skill',skillId:sid});
          if(r.state){state=r.state;syncHud();}
          toastModal('技能解锁',CFG.skills.skills.find(s=>s.id===sid)?.name+' 已解锁（-300 钻石）');
          renderRole();
        }catch(e){toastModal('解锁失败',e.message);}
        return;
      }
      if(window._bdDraft.length>=4){toastModal('槽位已满','先卸下一个技能再装配');return;}
      window._bdDraft.push(sid);renderRole();
    });
    // 卸下：点槽位
    body.querySelectorAll('.skill-card[data-slot]').forEach(el=>el.onclick=()=>{
      window._bdDraft.splice(parseInt(el.dataset.slot),1);renderRole();
    });
    // 提交 BD
    const sb=$('submitBD');
    if(sb)sb.onclick=async()=>{
      try{
        const r=await api('/api/player/action',{userId:uid(),action:'submit_bd',slots:window._bdDraft});
        if(r.state){state=r.state;await refreshStats();syncHud();}
        window._bdDraft=null;
        toastModal('BD 已保存',(r.slots||[]).length+' 个技能已生效');
        renderRole();
      }catch(e){toastModal('保存失败',e.message);}
    };
  }
  if(roleTab==='equip'){
    // 穿戴/卸下
    body.querySelectorAll('.eq-toggle').forEach(b=>b.onclick=async()=>{
      const e=(state.equip||[]).find(x=>x.id===b.dataset.eqid);
      const act=e&&e.equipped?'unequip':'equip';
      try{
        const r=await api('/api/player/action',{userId:uid(),action:act,itemId:b.dataset.eqid});
        if(r.state){state=r.state;await refreshStats();syncHud();}
        toastModal(act==='equip'?'已穿戴':'已卸下',b.dataset.eqid);
        renderRole();
      }catch(err){toastModal('操作失败',err.message);}
    });
    // 强化
    body.querySelectorAll('.eq-enh').forEach(b=>b.onclick=async()=>{
      try{
        const r=await api('/api/player/action',{userId:uid(),action:'enhance',slot:b.dataset.slot});
        if(r.state){state=r.state;await refreshStats();syncHud();}
        toastModal('强化成功',b.dataset.slot+' +'+(r.enhanceLevel||'?'));
        renderRole();
      }catch(err){toastModal('强化失败',err.message);}
    });
  }
  if(roleTab==='class'){
    let pick=c.classId;
    body.querySelectorAll('.class-card').forEach(el=>el.onclick=()=>{
      body.querySelectorAll('.class-card').forEach(x=>{x.classList.remove('sel');x.querySelector('.chk').textContent='';});
      el.classList.add('sel');el.querySelector('.chk').textContent='✓';pick=el.dataset.cls;
    });
    $('doTransfer').onclick=async()=>{
      try{
        const r=await api('/api/transfer',{userId:uid(),to:pick});
        if(r.state){state=r.state;await refreshStats();syncHud();}
        toastModal('转职成功',r.message||'装备属性已按新职业换算');renderRole();
      }catch(e){toastModal('转职失败',e.message);}
    };
  }
}
function renderWarehouse(){
  const body=$('panelBody');
  const inHome=homeSub==='warehouse';
  const backHome=inHome?'<button class="sub-back" id="backToHome">‹ 返回家园</button>':'';
  // 装备详情子层（仍在仓库面板内）
  if(equipDetailSlot){
    const eq=state&&state.equip?state.equip:[];
    const e=eq.find(x=>x.slot===equipDetailSlot)||{slot:equipDetailSlot,name:'未装备',enhance:0};
    const meta={weapon:['武器','🗡️'],helmet:['头盔','🪖'],armor:['护甲','🛡️'],trinket:['饰品','💍']}[equipDetailSlot]||['装备','◈'];
    const lv=e.enhance||0;
    body.innerHTML=`
      ${backHome}
      <div class="equip-detail">
        <div class="ed-head">
          <div class="ed-ico">${meta[1]}</div>
          <div><div class="ed-name">${e.name||meta[0]+' · +'+lv}</div><div class="ed-meta">部位：${meta[0]} ｜ 强化等级 +${lv}</div></div>
          <button class="ed-back" id="edBack">‹ 返回仓库</button>
        </div>
        <div class="sec-title">装备属性</div>
        ${attrRows()}
        <div class="enhance-bar">
          <div class="elbl"><span>强化进度</span><span>+${lv} → +${lv+1}</span></div>
          <div class="enhance-track"><i style="width:${Math.min(100,(lv+1)*18)}%"></i></div>
          <button class="big-btn goldbtn" id="doEnhance">强化 · 消耗 1,200 金币</button>
          <div class="strength-note">强化成功率 100%（+5 之前不失败）。数值为展示占位，待 playtest 调表。</div>
        </div>
      </div>`;
    $('edBack').onclick=()=>{equipDetailSlot=null;renderWarehouse();};
    if(inHome)$('backToHome').onclick=()=>{equipDetailSlot=null;homeSub=null;renderHome();};
    $('doEnhance').onclick=async()=>{
      try{
        const r=await api('/api/player/action',{userId:uid(),action:'enhance',slot:equipDetailSlot});
        if(r.state){state=r.state;await refreshStats();syncHud();}
        toastModal('强化成功',equipDetailSlot+' 强化至 +'+(r.enhanceLevel||'?'));
        renderWarehouse();
      }catch(e){toastModal('强化失败',e.message);}
    };
    // 穿戴/卸下按钮
    const isEquipped=eq.some(x=>x.slot===equipDetailSlot&&x.equipped);
    const eObj=eq.find(x=>x.slot===equipDetailSlot);
    if(eObj){
      const toggle=document.createElement('button');
      toggle.className='big-btn'; toggle.style.marginTop='8px';
      toggle.textContent=isEquipped?'卸下装备':'穿戴装备';
      toggle.onclick=async()=>{
        try{
          const r=await api('/api/player/action',{userId:uid(),action:isEquipped?'unequip':'equip',itemId:eObj.id});
          if(r.state){state=r.state;await refreshStats();syncHud();}
          toastModal(isEquipped?'已卸下':'已穿戴',eObj.id);renderWarehouse();
        }catch(err){toastModal('操作失败',err.message);}
      };
      $('doEnhance').parentNode.appendChild(toggle);
    }
    return;
  }
  // 真实仓库数据：材料/消耗品来自 state.warehouse，装备来自 state.equip（未穿戴=在仓库）
  const wh=state?.warehouse||[];
  const eqAll=state?.equip||[];
  const eqInBag=eqAll.filter(e=>!e.equipped);
  const matItems=wh.map(it=>({id:it.id,name:it.id,qty:it.qty,kind:it.kind||'材料',isEq:false}));
  const eqItems=eqInBag.map(e=>({id:e.id,name:e.id+' +'+(e.enhance||0),qty:1,kind:'装备_'+e.slot,isEq:true,slot:e.slot}));
  const allItems=[...eqItems,...matItems];
  const colOf=k=>k.startsWith('装备')?'#b8862f':k==='消耗品'?'#5fd38a':'#8892a4';
  let filter='all';
  const renderGrid=()=>{
    const shown=allItems.filter(it=>filter==='all'||(filter==='equip'&&it.isEq)||(filter==='material'&&!it.isEq&&it.kind!=='消耗品')||(filter==='consume'&&it.kind==='消耗品'));
    const grid=body.querySelector('.wg-grid');
    if(!grid)return;
    grid.innerHTML=shown.length?shown.map(it=>`
      <div class="wg-cell ${it.isEq?'equip':''}" data-id="${it.id}" data-eq="${it.isEq?it.slot:''}" data-kind="${it.kind}">
        <div class="wgi" style="background:${colOf(it.kind)}">${it.isEq?'🗡️':'◈'}</div>${it.name}<div class="wq">x${it.qty}</div>
      </div>`).join(''):'<div class="sec-sub">此类物品为空</div>';
    grid.querySelectorAll('.wg-cell[data-id]').forEach(el=>el.onclick=()=>{
      const it=allItems.find(x=>x.id===el.dataset.id);
      if(el.dataset.eq){equipDetailSlot=el.dataset.eq;renderWarehouse();}
      else if(it){
        // 材料：显示取出/存入操作
        const bag=state?.ext?.bag||{};
        const bagQty=bag[it.id]||0;
        toastModal(it.name,'仓库 '+it.qty+' · 背包 '+bagQty+'（存取请用下方按钮）');
      }
    });
  };
  body.innerHTML=`
    ${backHome}
    <div class="sec-title">仓库　<span class="sec-sub" style="display:inline">已用 ${allItems.length} / 200 格</span></div>
    <div class="sec-sub">点击金边装备查看/强化/穿戴；材料可存取（服务端权威）</div>
    <div class="filter-tabs" id="whFilter"><button class="on" data-f="all">全部</button><button data-f="equip">装备</button><button data-f="material">材料</button><button data-f="consume">消耗品</button></div>
    <div class="wg-grid"></div>
    <div style="display:flex;gap:8px;margin-top:12px">
      <input id="whItemId" placeholder="物品ID" style="flex:1;min-width:0;background:#1b2436;color:#e8e8e8;border:1px solid #3a4a63;padding:6px;border-radius:6px">
      <input id="whQty" type="number" value="1" min="1" style="width:56px;background:#1b2436;color:#e8e8e8;border:1px solid #3a4a63;padding:6px;border-radius:6px">
      <button class="btn-gray" id="whDeposit">存入</button><button class="btn-gray" id="whWithdraw">取出</button>
    </div>`;
  body.querySelectorAll('#whFilter button').forEach(b=>b.onclick=()=>{
    filter=b.dataset.f;
    body.querySelectorAll('#whFilter button').forEach(x=>x.classList.toggle('on',x===b));
    renderGrid();
  });
  const whAction=async(action)=>{
    const id=$('whItemId').value.trim(), qty=parseInt($('whQty').value)||1;
    if(!id){toastModal('提示','请输入物品ID');return;}
    try{
      const r=await api('/api/player/action',{userId:uid(),action,itemId:id,qty});
      if(r.state){state=r.state;syncHud();}
      toastModal(action==='deposit'?'存入成功':'取出成功',id+' x'+qty);
      renderPanel();
    }catch(e){toastModal('操作失败',e.message);}
  };
  $('whDeposit').onclick=()=>whAction('deposit');
  $('whWithdraw').onclick=()=>whAction('withdraw');
  if(inHome)$('backToHome').onclick=()=>{homeSub=null;renderHome();};
  renderGrid();
}
function renderMap(){
  const body=$('panelBody');
  const worlds=(CFG&&CFG.worlds&&CFG.worlds.worlds)||[];
  const unlocked=state&&state.world&&state.world.unlocked||['W1'];
  const cur=worldMap.worldId;
  const bgOf={grassland:'bg_explore_grassland',forest:'bg_explore_forest',desert:'bg_explore_desert',snow:'bg_explore_snow',cave:'bg_explore_cave',arena:'bg_battle_arena'};
  body.innerHTML=`
    <div class="sec-title">世界地图 · 6 大世界</div>
    <div class="sec-sub">当前：${cur} · 主线链式推进，通道成环。点已解锁世界可直接传送（免费 · 5s CD）</div>
    ${worlds.map(w=>{
      const isCur=w.id===cur, isUnlocked=unlocked.indexOf(w.id)>=0;
      const bg=bgOf[w.theme]||'bg_explore_grassland';
      return `<div class="preview-box world-map-item ${isCur?'cur':''} ${isUnlocked?'':'locked'}" data-world="${w.id}" style="height:88px;background-image:linear-gradient(rgba(8,13,22,.6),rgba(8,13,22,.85)),url(${RES}v3/backgrounds/${bg}.png);background-size:cover;background-position:center;cursor:${isUnlocked&&!isCur?'pointer':'default'}">
        <div class="pt">${w.id} · ${w.name} ${isCur?'<span style="font-size:11px;color:#7fe0a0">· 当前</span>':isUnlocked?'<span style="font-size:11px;color:#8fd0ff">· 已解锁</span>':'<span style="font-size:11px;color:#aaa">· 未解锁</span>'}</div>
        <div class="sec-sub">需 Lv.${w.level} ｜ ${isUnlocked?'可探索':'沿通道推进解锁'}</div>
      </div>`;
    }).join('')}
    <div class="sec-sub" style="margin-top:12px">💡 也可在大世界中找到发光的「⇄ 世界通道」直接传送。</div>`;
  body.querySelectorAll('.world-map-item').forEach(el=>{
    el.onclick=()=>{
      const wid=el.dataset.world;
      if(wid===cur)return;
      if(unlocked.indexOf(wid)<0){toastModal('尚未解锁','请沿世界通道推进解锁 '+wid);return;}
      closePanel();
      travelTo({to:wid,requireLevel:0,label:wid});
    };
  });
}
function renderIdle(){
  const body=$('panelBody');
  const inHome=homeSub==='idle';
  const backHome=inHome?'<button class="sub-back" id="backToHome">‹ 返回家园</button>':'';
  body.innerHTML=`
    ${backHome}
    <div class="sec-title">挂机设置</div>
    <div class="sec-sub">选择挂机地图 / 查看产出效率 / 领取离线收益</div>
    <div class="preview-box"><div class="pt">挂机地图</div><div class="sec-sub">翡翠草原 · 推荐 Lv.40-50 · 效率 ★★★☆</div></div>
    <div class="preview-box"><div class="pt">产出效率（每小时）</div><div class="sec-sub">经验 ~1,200 ｜ 金币 ~640 ｜ 材料 ~5</div></div>
    <div class="preview-box"><div class="pt">封顶时长</div><div class="sec-sub">8 小时（超出部分不再累计）</div></div>
    <button class="big-btn goldbtn" id="settleIdle">领取离线收益</button>`;
  if(inHome)$('backToHome').onclick=()=>{homeSub=null;renderHome();};
  $('settleIdle').onclick=async()=>{
    try{
      const r=await api('/api/idle/finalize',{userId:uid()});
      if(r.state){state=r.state;await refreshStats();syncHud();}
      closePanel(); showIdleReward(r.result);
    }catch(e){toastModal('结算失败',e.message);}
  };
}

/* ============================================================
   弹窗（遇怪 / 结算 / 收益 / 提示）
   ============================================================ */
function toastModal(title,sub){
  showModal(`<h2>${title}</h2><div class="msub">${sub||''}</div><button class="big-btn" id="toastOk">确定</button>`);
  $('toastOk').onclick=()=>{ hideModal(); if(view==='world'&&panel) renderPanel(); };
}
// 点击地图上的怪物 → 交战确认（单出口：交战进战斗 / 绕行回地图）
function askEncounter(mon){
  if(view!=='world'||panel||battlePlaying)return;
  currentMonster=mon;
  worldMap.active=false;
  showModal(`<h2>遭遇 ${mon.name}！</h2>
    <div class="msub">怪物进入警戒范围 · 是否交战？<br>战胜后有概率掉落宝箱</div>
    <div class="divider"></div>
    <div class="enc-btns"><button class="btn-red" id="encFight">交 战</button><button class="btn-gray" id="encFlee">绕 行</button></div>`);
  $('encFight').onclick=()=>{ hideModal(); doFight(mon); };
  $('encFlee').onclick=()=>{ hideModal(); currentMonster=null; worldMap.active=true; };
}
// 宝箱开启：调服务端权威接口入账（经验/金币/钻石/材料 → 存档）
async function onChestOpened(chest){
  worldMap.active=false;   // 开箱动画期间冻结操作
  let reward=null;
  try{
    const r=await api('/api/chest/open',{userId:uid(),worldId:worldMap.worldId,nodeId:chest.nodeId,tier:chest.tier});
    reward=r.reward;
    if(r.state){state=r.state;await refreshStats();syncHud();}
  }catch(e){ reward=null; }
  const label={wood:'木质宝箱',silver:'银质宝箱',gold:'黄金宝箱'}[chest.tier]||'宝箱';
  setTimeout(()=>{
    const rw=reward||{exp:0,gold:0,gem:0,material:0};
    showModal(`<h2>${label} · 开启</h2>
      <div class="msub">${reward?'服务端权威结算 · 已入账':'宝箱冷却中或暂不可开'}</div>
      <div class="divider"></div>
      <div class="reward-row"><span class="rk">经验</span><span class="rv">+${fmt(rw.exp)}</span></div>
      <div class="reward-row"><span class="rk">金币</span><span class="rv">+${fmt(rw.gold)}</span></div>
      <div class="reward-row"><span class="rk">钻石</span><span class="rv">+${fmt(rw.gem)}</span></div>
      <div class="reward-row"><span class="rk">材料</span><span class="rv">+${fmt(rw.material)}</span></div>
      <button class="big-btn" id="chestOk">收下 · 继续探索</button>`);
    $('chestOk').onclick=()=>{ hideModal(); worldMap.active=true; if(panel)renderPanel(); };
  },520); // 等开箱动画播完
}
function showBattleResult(rep,mon){
  const win=rep.result==='win';
  const drops=(rep.drops||[]).map(d=>`<div class="reward-row"><span class="rk">装备掉落</span><span class="rv g">${d.slot}</span></div>`).join('');
  showModal(`<h2 class="${win?'':'fail'}">${win?'战斗胜利':'战斗失败'}</h2>
    <div class="msub">${win?'击败':'被击败于'} ${mon.name} · 用时 ${rep.seconds||0} 秒</div>
    <div class="divider"></div>
    ${win?`
      <div class="reward-row"><span class="rk">经验</span><span class="rv">+${fmt(rep.exp)}</span></div>
      <div class="reward-row"><span class="rk">金币</span><span class="rv">+${fmt(rep.gold)}</span></div>
      ${drops}
      <button class="big-btn" id="resBtn">领取奖励 · 返回大世界</button>`:`
      <div class="ghost-line">变强指引<br>① 强化装备　② 调整技能 BD　③ 转职</div>
      <button class="big-btn" id="resBtn">返回大世界</button>`}`);
  $('resBtn').onclick=()=>{
    // 胜利：怪物从世界点位消失（服务端权威冷却）+ 本地掉落宝箱动画
    if(win && currentMonster){
      worldMap.dropChestFrom(currentMonster);
      try{api('/api/monster/kill',{worldId:worldMap.worldId,nodeId:currentMonster.nodeId});}catch(e){}
    }
    hideModal();
    currentMonster=null;
    showView('world');
  };  // 结算唯一出口 → 大世界
}
function showIdleReward(res){
  showModal(`<h2>离线收益</h2>
    <div class="msub">离开 8 小时 24 分钟 · 已达 8 小时上限，按上限结算</div>
    <div class="divider"></div>
    <div class="reward-row"><span class="rk">经验</span><span class="rv">+${fmt(res&&res.exp)}</span></div>
    <div class="reward-row"><span class="rk">金币</span><span class="rv">+${fmt(res&&res.gold)}</span></div>
    <div class="reward-row"><span class="rk">装备</span><span class="rv g">x${res&&res.equip||0}</span></div>
    <div class="reward-row"><span class="rk">材料</span><span class="rv g">x${res&&res.material||0}</span></div>
    <button class="big-btn" id="idleClaim">一键领取 · 返回大世界</button>`);
  $('idleClaim').onclick=()=>{ hideModal(); showView('world'); };
}

/* ============================================================
   战斗自动演出（战报驱动，玩家零输入）
   ============================================================ */
function floatDmg(text,x,y,heal){
  const d=document.createElement('div');
  d.className='dmg-float'+(heal?' heal':'');d.textContent=text;
  d.style.left=x+'px';d.style.top=y+'px';
  $('fxLayer').appendChild(d);
  setTimeout(()=>d.remove(),920);
}
function logLine(cls,text){
  const d=document.createElement('div');d.className='lg '+cls;d.textContent=text;
  $('battleLog').appendChild(d);
  $('battleLog').scrollTop=$('battleLog').scrollHeight;
}
function skillSlots(bd){
  const all=CFG.skills.skills;
  const colors=['sk-fire','sk-ice','sk-heal','sk-shield','sk-basic'];
  const items=bd.map(sid=>all.find(s=>s.id===sid)||{name:'技能',id:sid});
  items.push({name:'普攻',id:'basic'});
  $('skillSlots').innerHTML=items.map((s,i)=>`<div class="skill-slot ${colors[i%colors.length]}">${s.name.charAt(0)}<small>${s.name}</small></div>`).join('');
  $('skillCap').textContent=`生效技能：已装备 ${bd.length} 技能 + 普攻兜底（自动释放）`;
}
async function playBattle(rep,mon,bd){
  battlePlaying=true;paused=false;
  currentMonster=mon;
  skillSlots(bd);
  $('battleLog').innerHTML='';
  $('monName').textContent=mon.name;
  $('autoTag').textContent='▶ 自动战斗中';$('btPause').textContent='暂停';
  showView('battle');
  const hMax=Math.max(50,...rep.rounds.map(r=>r.heroHp));
  const mMax=Math.max(50,...rep.rounds.map(r=>r.monHp));
  bHp={hero:hMax,heroMax:hMax,mon:mMax,monMax:mMax};
  updateHp();
  logLine('sys',`服务端战报 seed=${rep.seed} · ${rep.rounds.length} 回合 · 自动战斗`);
  for(const r of rep.rounds){
    while(paused && view==='battle') await sleep(120);
    if(view!=='battle') break;  // 中途退出登录则中止
    const roundNo=Math.min(r.n,30);
    $('roundVal').textContent=`${roundNo} / 30`;
    if(r.actor==='hero'){
      setBAnim('hero','attack',420);await sleep(150);
      if(r.damage>0){
        setBAnim('mon','hit',300);
        spawnFx('-'+Math.round(r.damage),880,420,'#ff6b6b');
        logLine('hero',`R${r.n} ${r.skillName} → ${mon.name} -${Math.round(r.damage)}`);
      }else if(r.heal>0){
        spawnFx('+'+Math.round(r.heal),440,420,'#7fe0a0');
        logLine('hero',`R${r.n} ${r.skillName} → 恢复 +${Math.round(r.heal)}`);
      }else{
        logLine('hero',`R${r.n} ${r.skillName}（增益/护盾）`);
      }
    }else{
      setBAnim('mon','attack',420);await sleep(150);
      setBAnim('hero','hit',300);
      spawnFx('-'+Math.round(r.damage),440,420,'#ff6b6b');
      logLine('mon',`R${r.n} ${mon.name} → 英雄 -${Math.round(r.damage)}`);
    }
    bHp.hero=r.heroHp;bHp.mon=r.monHp;updateHp();
    await sleep(270);
  }
  battlePlaying=false;
  if(view!=='battle') return;  // 已退出登录
  const win=rep.result==='win';
  if(win){setBAnim('mon','die',600);spawnFx('+EXP',880,400,'#f0d590');spawnFx('+GOLD',840,370,'#ffd76b');}
  else setBAnim('hero','die',600);
  const toast=document.createElement('div');
  toast.className='toast-win'+(win?'':' lose');toast.textContent=win?'战 斗 胜 利':'战 斗 失 败';
  $('fxLayer').appendChild(toast);setTimeout(()=>toast.remove(),1200);
  await sleep(1500);
  if(view!=='battle') return;
  showBattleResult(rep,mon);
}
function updateHp(){
  $('monHpFill').style.width=Math.max(0,bHp.mon/bHp.monMax*100)+'%';
  $('heroHpFill').style.width=Math.max(0,bHp.hero/bHp.heroMax*100)+'%';
  $('monHpNum').textContent=`${Math.ceil(bHp.mon)} / ${Math.ceil(bHp.monMax)}`;
  $('heroHpNum').textContent=`${Math.ceil(bHp.hero)} / ${Math.ceil(bHp.heroMax)}`;
}

/* ============================================================
   事件绑定（严格按通道）
   ============================================================ */
// 登录页
$('loginBtn').onclick=doLogin;
$('registerBtn').onclick=doRegister;
$('deviceLoginBtn').onclick=doDeviceLogin;
$('enterBtn').onclick=enterGame;
$('switchBtn').onclick=switchAccount;
$('authPwd').addEventListener('keydown',e=>{if(e.key==='Enter')doLogin();});
// 退出登录（唯一出口，全局）
$('logoutBtn').onclick=doLogout;
// 底部 5 入口导航（v1.6，唯一面板入口）
document.querySelectorAll('.nav-badge').forEach(b=>b.onclick=()=>openPanel(b.dataset.panel));
// 面板唯一出口
$('panelBack').onclick=closePanel;
$('panelShade').onclick=closePanel;
// 交战：服务端权威结算 → 战斗演出（由 askEncounter 的"交战"按钮触发）
async function doFight(mon){
  if(view!=='world'||panel||battlePlaying)return;
  try{
    const bd=(state&&state.skills&&state.skills.slots)||['slash'];
    const bundle=await api('/api/battle/fight',{userId:uid(),bd,monsterId:mon.monId||mon.type,monsterLv:5,elite:mon.elite?'elite':''});
    if(bundle.state){state=bundle.state;await refreshStats();syncHud();}
    currentReport=bundle.report;
    await playBattle(bundle.report,mon,bd);
  }catch(e){toastModal('交战失败',e.message);worldMap.active=true;}
}
// 战斗控制
$('btPause').onclick=()=>{paused=!paused;$('btPause').textContent=paused?'继续':'暂停';$('autoTag').textContent=paused?'⏸ 已暂停':'▶ 自动战斗中';};
// B05：战斗/面板期间页面失焦自动暂停战斗时钟，防止后台演出
document.addEventListener('visibilitychange',()=>{
  if(document.hidden && battlePlaying && !paused){
    paused=true;$('btPause').textContent='继续';$('autoTag').textContent='⏸ 已暂停（切出页面）';
  }
});

/* ============================================================
   初始化
   ============================================================ */
async function init(){
  fitStage();
  try{
    await preload();
    CFG=await api('/api/config');
    const restored=restoreSession();
    refreshLoginView();
    showView('login');              // 启动永远停在登录页（唯一入口）
    requestAnimationFrame(renderLoop);
    $('loading').classList.add('hidden');
  }catch(e){
    $('loadTxt').textContent='加载失败：'+e.message;
  }
}
init();
})();

/* ============================================================
   杖剑传说 · 剧情主线 + 每日委托
   - 第一章保留原有 ID、目标、奖励和本地存档，老玩家无缝进入第二章
   - 第二章由家园 NPC 串联：雾核 → 微光药剂 → 星图残页 → 赤焰峡谷
   - 每日进度与领取记录完全读取服务端存档，金币/经验按领奖等级成长
   - 渲染文本统一经过 esc() 转义，与线上 v1.9.92 安全策略保持一致
   ============================================================ */
(function(){
'use strict';

const QDEFS=[
  {id:'q_kill3',chapter:1,name:'初试身手',desc:'击败 3 只怪物',goal:3,kind:'kill',ico:'sword',reward:'800 金币 · 30 钻石 · 300 经验',
    storyAfter:'艾拉：平原上的魔物开始变得焦躁，这不是寻常的兽潮。'},
  {id:'q_chest1',chapter:1,name:'意外之财',desc:'开启 1 个宝箱',goal:1,kind:'chest',ico:'chest',reward:'600 金币 · 20 钻石 · 200 经验 · 2 材料',
    storyAfter:'箱底压着一小片焦黑木屑，上面残留着与迷雾森林相同的寒气。'},
  {id:'q_equip4',chapter:1,name:'全副武装',desc:'穿戴 4 件装备',goal:4,kind:'equip',ico:'armor',reward:'900 金币 · 40 钻石 · 400 经验',
    storyAfter:'格罗姆：装备会记住使用者的意志。准备好后，就去看看森林里的异变。'},
  {id:'q_enhance1',chapter:1,name:'精益求精',desc:'强化装备 1 次',goal:1,kind:'enhance',ico:'hammer',reward:'700 金币 · 25 钻石 · 250 经验 · 1 材料',
    storyAfter:'强化后的武器映出一缕暗紫色光芒，远处的雾门也随之震动。'},
  {id:'q_travel',chapter:1,name:'走出草原',desc:'抵达迷雾森林（W2）',goal:1,kind:'travel',ico:'map',reward:'1,500 金币 · 80 钻石 · 800 经验 · 5 材料',
    storyAfter:'你穿过雾门时，一枚微弱的星纹在掌心亮起。艾拉请你先回家园详谈。'},

  {id:'q2_eva_talk',chapter:2,name:'归园议事',desc:'与家园的艾拉交谈',goal:1,kind:'c2_talk_eva',trigger:'talk_eva',ico:'book',npc:'eva',speaker:'艾拉·守望者',reward:'300 金币 · 10 钻石 · 100 经验',
    storyBefore:'你掌心的星纹与古籍记载一致。森林深处的黑雾并非天然形成，而是一枚“雾核”在吞噬地脉。请先帮米娅收集药草，她能让雾核短暂显形。',
    storyAfter:'艾拉展开残旧星图：雾核只是封印的一角，真正的秘密似乎藏在更南方的赤色荒地。'},
  {id:'q2_herb3',chapter:2,name:'微光药剂',desc:'采集草药 3 次',goal:3,kind:'c2_herb',trigger:'herb',ico:'potion',npc:'mia',speaker:'米娅·药师',reward:'600 金币 · 15 钻石 · 200 经验 · 1 材料',
    storyBefore:'黑雾会遮蔽雾核的轮廓。我需要三份新鲜草药调制微光药剂，普通地图上的草药就可以。',
    storyAfter:'米娅将药剂滴入星纹，淡金色的涟漪扩散开来：现在，你能看见雾中魔物守护的痕迹了。'},
  {id:'q2_w2_win5',chapter:2,name:'雾中试锋',desc:'在迷雾森林赢得 5 场大世界战斗',goal:5,kind:'c2_kill_w2',trigger:'kill_w2',ico:'sword',npc:'eva',speaker:'艾拉·守望者',reward:'1,200 金币 · 30 钻石 · 500 经验 · 2 材料',
    storyBefore:'药剂已经生效。前往迷雾森林清理被雾核操控的魔物，留意它们身后是否留有星图纸片。',
    storyAfter:'最后一只魔物倒下后，你在消散的雾气中找到半张烧焦的星图，边缘刻着陌生的赤色符号。'},
  {id:'q2_w2_chest2',chapter:2,name:'残页回响',desc:'在迷雾森林开启 2 个宝箱',goal:2,kind:'c2_chest_w2',trigger:'chest_w2',ico:'chest',npc:'eva',speaker:'艾拉·守望者',reward:'1,000 金币 · 25 钻石 · 400 经验 · 3 材料',
    storyBefore:'星图残片之间会彼此吸引。雾中的宝箱可能保存着其余部分，试着把它们找回来。',
    storyAfter:'残页拼合后，一座被烈焰包围的古代熔炉浮现出来。格罗姆认出那是早已失传的星火锻炉。'},
  {id:'q2_enhance1',chapter:2,name:'重铸星火',desc:'成功强化装备 1 次',goal:1,kind:'c2_enhance',trigger:'enhance',ico:'hammer',npc:'grom',speaker:'格罗姆·铁匠',reward:'900 金币 · 20 钻石 · 300 经验 · 2 材料',
    storyBefore:'星图上的金属粉末来自星火锻炉。让我为你的装备再强化一次，否则它承受不了峡谷的热流。',
    storyAfter:'格罗姆敲下最后一锤，装备表面掠过一道金红纹路：成了，这件装备现在能抵御星火灼烧。'},
  {id:'q2_travel_w3',chapter:2,name:'追随赤痕',desc:'抵达赤焰峡谷（W3）',goal:1,kind:'c2_travel_w3',trigger:'travel_w3',ico:'map',npc:'eva',speaker:'艾拉·守望者',reward:'2,200 金币 · 80 钻石 · 1,200 经验 · 8 材料',
    storyBefore:'装备已经准备妥当。沿星图上的赤痕前往赤焰峡谷，确认古代熔炉为何重新苏醒。',
    storyAfter:'你踏入峡谷时，远方熔炉骤然点亮。一个低沉的声音穿过热浪：“守望者，你终于回来了。”'}
];

const DAILY_DEFS=[
  {id:'d_battle5',name:'今日讨伐',desc:'赢得 5 场战斗（大世界或副本）',goal:5,kind:'battle',ico:'sword',reward:{gold:800,gem:10,exp:200,material:0}},
  {id:'d_chest2',name:'今日寻宝',desc:'成功开启 2 个宝箱',goal:2,kind:'chest',ico:'chest',reward:{gold:500,gem:5,exp:100,material:1}},
  {id:'d_herb3',name:'今日采集',desc:'成功采集 3 次草药',goal:3,kind:'herb',ico:'potion',reward:{gold:400,gem:5,exp:100,material:1}}
];

let uid='',st={progress:{},claimed:{}},hooks={},lastState=null;
const busy=new Set();
const $=id=>document.getElementById(id);
const CHAPTER_NAMES={1:'初入青草平原',2:'雾核与星火'};

function esc(s){return String(s==null?'':s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));}
function key(){return 'zjcs_quest_'+uid;}
function load(){
  try{st=JSON.parse(localStorage.getItem(key())||'null')||{};}catch(e){st={};}
  st.progress=st.progress||{};st.claimed=st.claimed||{};
}
function save(){try{localStorage.setItem(key(),JSON.stringify(st));}catch(e){}}
function activeIdx(){for(let i=0;i<QDEFS.length;i++)if(!st.claimed[QDEFS[i].id])return i;return -1;}
function done(q){return (st.progress[q.kind]||0)>=q.goal;}
function dailyData(){
  const d=lastState&&lastState.ext&&lastState.ext.dailyQuests;
  return d&&typeof d==='object'?d:{progress:{},claimed:{}};
}
function dailyProgress(q){return Math.floor(Number((dailyData().progress||{})[q.kind])||0);}
function dailyDone(q){return dailyProgress(q)>=q.goal;}
function dailyClaimed(q){return (dailyData().claimed||{})[q.id]===true;}
function level(){return Math.max(1,Math.floor(Number(lastState&&lastState.core&&lastState.core.level)||1));}
function dailyMultiplier(lv){return 1+(Math.max(1,lv||level())-1)*0.1;}
function scaledDailyReward(q,lv){
  const m=dailyMultiplier(lv),r=q.reward;
  return {gold:Math.floor(r.gold*m+1e-9),gem:r.gem,exp:Math.floor(r.exp*m+1e-9),material:r.material};
}
function rewardText(r){
  const parts=[];
  if(r.gold)parts.push(Math.floor(r.gold).toLocaleString()+' 金币');
  if(r.gem)parts.push(Math.floor(r.gem)+' 钻石');
  if(r.exp)parts.push(Math.floor(r.exp).toLocaleString()+' 经验');
  if(r.material)parts.push(Math.floor(r.material)+' 材料');
  return parts.join(' · ');
}
function chapterProgress(chapter){
  const list=QDEFS.filter(q=>q.chapter===chapter);
  return {done:list.filter(q=>st.claimed[q.id]).length,total:list.length};
}
function questIcon(q,ok){return window.ICON&&ICON[q.ico]?ICON[q.ico](22,ok?'#7fe0a0':'#f0d590'):'';}

function mainItem(q,next){
  const p=Math.min(st.progress[q.kind]||0,q.goal),ok=done(q)&&!next,waiting=busy.has(q.id);
  return `<div class="qt-item${next?' next':''}${ok?' done':''}" data-q="${esc(q.id)}" title="奖励：${esc(q.reward)}">
    <span class="qt-ico">${questIcon(q,ok)}</span><div class="qt-main"><div class="qt-name">${esc(q.name)}</div>
    <div class="qt-desc">${esc(q.desc)}</div><div class="qt-bar${p>=q.goal?' full':''}"><i style="width:${Math.round(p/q.goal*100)}%"></i></div></div>
    <span class="qt-right">${ok?`<b class="qt-claim${waiting?' busy':''}">${waiting?'领取中':'领取'}</b>`:`<em>${p}/${q.goal}</em>`}</span></div>`;
}
function dailyItem(q){
  const p=Math.min(dailyProgress(q),q.goal),claimed=dailyClaimed(q),ok=dailyDone(q)&&!claimed,waiting=busy.has(q.id);
  const rw=scaledDailyReward(q),title=(claimed?'今日已领取：':'当前等级奖励：')+rewardText(rw);
  return `<div class="qt-item daily${ok?' done':''}${claimed?' claimed':''}" data-q="${esc(q.id)}" title="${esc(title)}">
    <span class="qt-ico">${questIcon(q,ok||claimed)}</span><div class="qt-main"><div class="qt-name">${esc(q.name)}</div>
    <div class="qt-desc">${esc(q.desc)}</div><div class="qt-bar${p>=q.goal?' full':''}"><i style="width:${Math.round(p/q.goal*100)}%"></i></div></div>
    <span class="qt-right">${claimed?'<em>已领</em>':ok?`<b class="qt-claim${waiting?' busy':''}">${waiting?'领取中':'领取'}</b>`:`<em>${p}/${q.goal}</em>`}</span></div>`;
}

function render(){
  const outer=$('questTracker'),box=$('qtBody')||outer;
  if(!outer||!box)return;
  const ai=activeIdx();let html='';
  outer.classList.toggle('qt-all-done',ai<0);
  if(ai<0){
    html+=`<div class="qt-head"><span>主线 · 两章完成</span><em>${QDEFS.length}/${QDEFS.length}</em></div>
      <div class="qt-all">星火锻炉已经苏醒 · 新的冒险即将展开</div>`;
  }else{
    const q=QDEFS[ai],cp=chapterProgress(q.chapter);
    html+=`<div class="qt-head"><span>主线 · 第${q.chapter===1?'一':'二'}章 · ${esc(CHAPTER_NAMES[q.chapter])}</span><em>${cp.done}/${cp.total}</em></div>`;
    html+=QDEFS.slice(ai,ai+2).map((item,i)=>mainItem(item,i>0)).join('');
  }
  const mult=dailyMultiplier().toFixed(1).replace('.0','');
  html+=`<div class="qt-section"><span>每日委托</span><em>Lv.${level()} · 金币/经验 ×${mult} · 0点重置</em></div>`;
  html+=DAILY_DEFS.map(dailyItem).join('');
  box.innerHTML=html;
  box.querySelectorAll('.qt-claim').forEach(b=>{
    b.onclick=ev=>{ev.stopPropagation();const id=b.closest('.qt-item').dataset.q;if(!busy.has(id))claim(id);};
  });
}

async function refreshFromServer(){
  if(!hooks.refresh)return;
  try{const s=await hooks.refresh();if(s)syncState(s);}catch(e){}
}

async function claim(qid){
  const daily=DAILY_DEFS.find(q=>q.id===qid),q=QDEFS.find(x=>x.id===qid);
  if(busy.has(qid))return;
  if(daily&&(!dailyDone(daily)||dailyClaimed(daily)))return;
  if(q){
    const ai=activeIdx();
    if(!done(q)||st.claimed[qid]||ai<0||QDEFS[ai].id!==qid)return;
  }
  if(!daily&&!q)return;
  busy.add(qid);render();
  try{
    const r=await hooks.claim(qid);
    if(q){st.claimed[qid]=true;save();}
    if(r&&r.state){syncState(r.state);if(hooks.onState)hooks.onState(r.state);}
    const actual=daily?(r&&r.reward?r.reward:scaledDailyReward(daily)):null;
    const story=q&&q.storyAfter?`<br><span class="qt-story">${q.storyAfter}</span><br>`:'';
    hooks.toast&&hooks.toast('任务完成 · '+(q||daily).name,story+'奖励已入账：'+(q?q.reward:rewardText(actual)));
  }catch(e){
    const msg=(e&&e.message)||'';
    if(q&&msg.indexOf('已领取')>=0){
      st.claimed[qid]=true;
      if((st.progress[q.kind]||0)<q.goal)st.progress[q.kind]=q.goal;
      save();hooks.toast&&hooks.toast('任务已同步','「'+q.name+'」奖励此前已领取，进度已自动校准');
    }else if(daily&&(msg.indexOf('已领取')>=0||msg.indexOf('进度不足')>=0)){
      await refreshFromServer();
      hooks.toast&&hooks.toast('每日状态已刷新',msg||'已同步服务端最新进度');
    }else hooks.toast&&hooks.toast('领取失败',msg);
  }finally{busy.delete(qid);render();}
}

function syncState(s){
  if(!s)return;
  lastState=s;let changed=false;
  if(s.equip){
    const n=s.equip.filter(e=>e.equipped).length;
    if(n>(st.progress.equip||0)){st.progress.equip=n;changed=true;}
  }
  const unlocked=s.world&&s.world.unlocked||[];
  if((unlocked.includes('W3')||s.world&&s.world.worldId==='W3')&&(st.progress.c2_travel_w3||0)<1){
    st.progress.c2_travel_w3=1;changed=true;
  }
  const sq=s.ext&&s.ext.quests;
  if(sq)QDEFS.forEach(q=>{
    if(sq[q.id]===true){
      if(!st.claimed[q.id]){st.claimed[q.id]=true;changed=true;}
      if((st.progress[q.kind]||0)<q.goal){st.progress[q.kind]=q.goal;changed=true;}
    }
  });
  if(changed)save();render();
}

window.Quest={
  init(userId,h){
    uid=userId||'guest';hooks=h||{};load();
    if(hooks.getState)syncState(hooks.getState());
    save();render();
  },
  progress(trigger,n){
    if(!trigger)return;let changed=false;
    QDEFS.forEach(q=>{if((q.trigger||q.kind)===trigger){st.progress[q.kind]=(st.progress[q.kind]||0)+(n==null?1:n);changed=true;}});
    if(changed){save();render();}
  },
  set(trigger,v){
    if(!trigger)return;let changed=false;
    QDEFS.forEach(q=>{if((q.trigger||q.kind)===trigger&&v>(st.progress[q.kind]||0)){st.progress[q.kind]=v;changed=true;}});
    if(changed){save();render();}
  },
  syncState,
  active(){const i=activeIdx();return i<0?null:QDEFS[i];},
  list(){return QDEFS;},
  dailyList(){return DAILY_DEFS;},
  render
};
})();

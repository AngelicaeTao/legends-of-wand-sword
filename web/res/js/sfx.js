/* ============================================================
   杖剑传说 · 程序化音效（v1.9.6）
   ------------------------------------------------------------
   不引入任何音频文件：全部用 WebAudio 合成，零请求、零体积、可随时关闭。
   每种交互使用独立音色，避免"一个 click 走天下"的廉价感：
   - 界面：click / hover / tab / panelOpen / panelClose
   - 战斗：slash / enemySlash / hit / crit / hurt / cast / heal / buff
           victory / death / fail
   - 世界：gate / arrive / step / coin / chest / alert / flee
   - 成长：level / quest / equip / unequip / forge / rune / transfer
   - 经营：buy / sell / donate / build / bagIn / bagOut / settle
   - 系统：login / deny / pause / resume
   首次用户交互后自动 resume（浏览器自动播放策略）。
   ============================================================ */
(function(){
'use strict';
let ctx=null, muted=false, master=null;
try{ muted=localStorage.getItem('zjcs_muted')==='1'; }catch(e){}

function ac(){
  if(!ctx){
    try{
      ctx=new (window.AudioContext||window.webkitAudioContext)();
      master=ctx.createGain();master.gain.value=0.9;master.connect(ctx.destination);
    }catch(e){ return null; }
  }
  if(ctx.state==='suspended') ctx.resume();
  return ctx;
}
function env(g,t0,peak,dur,attack){
  const a=attack||0.012;
  g.gain.setValueAtTime(0.0001,t0);
  g.gain.exponentialRampToValueAtTime(Math.max(0.0002,peak),t0+a);
  g.gain.exponentialRampToValueAtTime(0.0001,t0+dur);
}
function tone(freq,dur,type,peak,slide,delay){
  const c=ac(); if(!c||muted)return;
  const t0=c.currentTime+(delay||0);
  const o=c.createOscillator(), g=c.createGain();
  o.type=type||'square';
  o.frequency.setValueAtTime(freq,t0);
  if(slide) o.frequency.exponentialRampToValueAtTime(Math.max(30,freq*slide),t0+dur);
  env(g,t0,peak||0.05,dur);
  o.connect(g).connect(master);
  o.start(t0);o.stop(t0+dur+0.02);
}
/* 噪声：type 滤波类型（lowpass/bandpass/highpass），freq 截止/中心频率，q 品质因数 */
function noise(dur,peak,type,freq,q,slideTo,delay){
  const c=ac(); if(!c||muted)return;
  const t0=c.currentTime+(delay||0);
  const len=Math.max(1,Math.floor(c.sampleRate*dur));
  const buf=c.createBuffer(1,len,c.sampleRate);
  const d=buf.getChannelData(0);
  for(let i=0;i<len;i++){ d[i]=(Math.random()*2-1)*(1-i/len); }
  const src=c.createBufferSource();src.buffer=buf;
  const f=c.createBiquadFilter();
  f.type=type||'lowpass';f.frequency.setValueAtTime(freq||1200,t0);
  if(q)f.Q.value=q;
  if(slideTo)f.frequency.exponentialRampToValueAtTime(Math.max(40,slideTo),t0+dur);
  const g=c.createGain();env(g,t0,peak||0.06,dur,0.006);
  src.connect(f).connect(g).connect(master);
  src.start(t0);
}
function seq(notes,gap,dur,type,peak,slide){
  notes.forEach((f,i)=>setTimeout(()=>tone(f,dur,type||'triangle',peak||0.05,slide),i*gap));
}

window.SFX={
  unlocked:()=>!!ctx,
  unlock(){ ac(); },
  get muted(){ return muted; },
  setMuted(v){
    muted=!!v;
    try{ localStorage.setItem('zjcs_muted',muted?'1':'0'); }catch(e){}
  },

  /* ---------- 界面 ---------- */
  click(){ tone(760,0.05,'square',0.035,0.85); },
  hover(){ tone(1040,0.028,'sine',0.016); },
  tab(){ tone(496,0.045,'triangle',0.03); noise(0.045,0.012,'highpass',2600); },
  panelOpen(){ tone(300,0.16,'sine',0.038,1.9); noise(0.12,0.014,'bandpass',900,1.2,2200); },
  panelClose(){ tone(520,0.15,'sine',0.034,0.48); noise(0.1,0.012,'bandpass',1200,1.2,420); },
  deny(){ tone(180,0.1,'square',0.045,0.8); tone(140,0.12,'square',0.04,0.85,0.09); },
  pause(){ tone(420,0.09,'triangle',0.03,0.7); },
  resume(){ tone(380,0.09,'triangle',0.03,1.3); },

  /* ---------- 战斗 ---------- */
  // 命中类带 ±6% 随机音高：连续战斗时不会变成"同一段采样循环"
  slash(){ const j=0.94+Math.random()*0.12; noise(0.13,0.05,'bandpass',1800*j,1.1,420); tone(260*j,0.1,'sawtooth',0.028,0.55); },
  enemySlash(){ const j=0.94+Math.random()*0.12; noise(0.15,0.055,'bandpass',800*j,0.9,260); tone(150*j,0.13,'sawtooth',0.03,0.6); },
  hit(){ const j=0.94+Math.random()*0.12; noise(0.14,0.075,'lowpass',900*j); tone(120*j,0.12,'sawtooth',0.05,0.6); },
  crit(){ const j=0.95+Math.random()*0.1; noise(0.2,0.095,'bandpass',1600*j,0.8,400); tone(90*j,0.18,'sawtooth',0.06,0.5); tone(1760*j,0.12,'sine',0.018,0.6,0.02); },
  hurt(){ const j=0.95+Math.random()*0.1; noise(0.12,0.055,'lowpass',520*j); tone(86*j,0.16,'triangle',0.055,0.55); },
  cast(){ seq([494,659,880],45,0.18,'sine',0.028,1.04); noise(0.2,0.012,'highpass',2200); },
  heal(){ seq([520,700,880],55,0.16,'sine',0.04); },
  buff(){ tone(174,0.4,'triangle',0.045,2.0,0); tone(261,0.34,'sine',0.025,1.8,0.05); },
  shield(){ tone(660,0.3,'triangle',0.03,1.0); tone(990,0.26,'sine',0.022,1.02,0.03); noise(0.22,0.016,'highpass',2600); },
  battleStart(){ noise(0.32,0.05,'lowpass',800,0.7,300); tone(110,0.3,'sawtooth',0.04,1.4); tone(220,0.18,'triangle',0.03,1.6,0.12); },
  victory(){ seq([523,659,784,1046],95,0.3,'triangle',0.055); noise(0.3,0.02,'highpass',1800); },
  death(){ tone(220,0.5,'sawtooth',0.05,0.42); tone(146,0.6,'sawtooth',0.045,0.5,0.14); noise(0.4,0.04,'lowpass',420); },
  fail(){ seq([320,240,180],85,0.25,'sawtooth',0.045); },

  /* ---------- 世界 ---------- */
  gate(){ tone(300,0.22,'sine',0.05,2.2); setTimeout(()=>tone(640,0.18,'triangle',0.045,1.4),140); },
  arrive(){ tone(110,0.35,'sine',0.06,1.5); noise(0.3,0.02,'lowpass',700); },
  step(){ noise(0.055,0.012,'lowpass',520+Math.random()*220); },
  coin(){ tone(1568,0.07,'square',0.026,1.02); tone(2093,0.09,'square',0.022,0.98,0.055); noise(0.05,0.012,'highpass',3000,1,0,0.02); },
  chest(){ seq([520,660,790,990],70,0.2,'triangle',0.05); },
  alert(){ tone(880,0.1,'square',0.036); tone(660,0.14,'square',0.033,0.95,0.11); },
  flee(){ noise(0.26,0.04,'bandpass',900,0.8,260); tone(520,0.2,'sine',0.025,0.5); },

  /* ---------- 成长 ---------- */
  level(){ seq([523,659,784,1046],90,0.28,'triangle',0.06); },
  quest(){ seq([784,988,1174],80,0.3,'sine',0.055); },
  equip(){ noise(0.07,0.028,'highpass',2400); tone(1180,0.09,'sine',0.022,0.86); },
  unequip(){ noise(0.12,0.03,'bandpass',1500,0.7,600); tone(420,0.1,'triangle',0.022,0.72); },
  forge(){ noise(0.07,0.06,'lowpass',900); tone(160,0.1,'square',0.045,0.6); setTimeout(()=>{noise(0.09,0.045,'lowpass',700);tone(1960,0.16,'sine',0.016,0.75);},150); },
  rune(){ seq([740,988,1318,988],52,0.14,'sine',0.03,1.02); },
  transfer(){ seq([392,523,659,880],70,0.24,'triangle',0.04,1.02); noise(0.25,0.02,'highpass',2600); },
  dungeon(){ noise(0.42,0.045,'lowpass',420,0.8,180); tone(74,0.5,'sine',0.05,1.3); tone(294,0.2,'triangle',0.022,0.9,0.18); },
  config(){ tone(587,0.07,'triangle',0.03); tone(784,0.1,'triangle',0.026,1.0,0.07); },

  /* ---------- 经营 ---------- */
  buy(){ tone(1318,0.06,'square',0.024); tone(1760,0.07,'square',0.02,0.98,0.05); noise(0.04,0.01,'highpass',3400,1,0,0.08); },
  sell(){ tone(1174,0.07,'square',0.024,0.9); tone(880,0.1,'square',0.02,0.85,0.06); },
  donate(){ seq([392,494,587,784],90,0.34,'sine',0.032); },
  build(){ noise(0.06,0.055,'lowpass',1100); tone(240,0.12,'triangle',0.04,0.7); setTimeout(()=>{noise(0.07,0.04,'lowpass',800);tone(180,0.14,'triangle',0.035,0.8);},135); },
  bagIn(){ noise(0.16,0.035,'lowpass',620); tone(120,0.14,'triangle',0.03,0.7); },
  bagOut(){ noise(0.16,0.032,'bandpass',1300,0.8,700); tone(300,0.12,'triangle',0.026,1.2); },
  settle(){ seq([659,880,1046],75,0.22,'triangle',0.04); },

  /* ---------- 系统 ---------- */
  login(){ seq([523,784,1046],110,0.3,'sine',0.04); }
};
window.SFX.alias={};   // 兼容旧调用，不再使用

/* 全局按钮点击音：捕获阶段，避免被各面板的 stopPropagation 吃掉 */
document.addEventListener('click',e=>{
  const el=e.target&&e.target.closest?e.target.closest('button,.nav-badge,.qt-claim'):null;
  if(el)window.SFX.click();
},true);
})();

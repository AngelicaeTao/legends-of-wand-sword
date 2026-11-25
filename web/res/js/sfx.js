/* ============================================================
   杖剑传说 · 程序化音效（v1.9.1）
   ------------------------------------------------------------
   不引入任何音频文件：全部用 WebAudio 合成，零请求、零体积、可随时关闭。
   - click 界面点击：短促方波
   - hit 战斗命中：噪声冲击 + 低频
   - chest 开箱：上行琶音
   - level 升级：三音上行
   - quest 任务完成：明亮和弦
   - gate 传送：下扫 + 上行
   首次用户交互后自动 resume（浏览器自动播放策略）。
   ============================================================ */
(function(){
'use strict';
let ctx=null, muted=false;
try{ muted=localStorage.getItem('zjcs_muted')==='1'; }catch(e){}

function ac(){
  if(!ctx){
    try{ ctx=new (window.AudioContext||window.webkitAudioContext)(); }catch(e){ return null; }
  }
  if(ctx.state==='suspended') ctx.resume();
  return ctx;
}
function env(g,t0,peak,dur){
  g.gain.setValueAtTime(0.0001,t0);
  g.gain.exponentialRampToValueAtTime(Math.max(0.0002,peak),t0+0.012);
  g.gain.exponentialRampToValueAtTime(0.0001,t0+dur);
}
function tone(freq,dur,type,peak,slide){
  const c=ac(); if(!c||muted)return;
  const t0=c.currentTime;
  const o=c.createOscillator(), g=c.createGain();
  o.type=type||'square';
  o.frequency.setValueAtTime(freq,t0);
  if(slide) o.frequency.exponentialRampToValueAtTime(Math.max(40,freq*slide),t0+dur);
  env(g,t0,peak||0.05,dur);
  o.connect(g).connect(c.destination);
  o.start(t0);o.stop(t0+dur+0.02);
}
function noise(dur,peak,cut){
  const c=ac(); if(!c||muted)return;
  const t0=c.currentTime;
  const len=Math.floor(c.sampleRate*dur);
  const buf=c.createBuffer(1,len,c.sampleRate);
  const d=buf.getChannelData(0);
  for(let i=0;i<len;i++){ d[i]=(Math.random()*2-1)*(1-i/len); }
  const src=c.createBufferSource();src.buffer=buf;
  const f=c.createBiquadFilter();f.type='lowpass';f.frequency.value=cut||1200;
  const g=c.createGain();env(g,t0,peak||0.06,dur);
  src.connect(f).connect(g).connect(c.destination);
  src.start(t0);
}
function seq(notes,gap,dur,type,peak){
  notes.forEach((f,i)=>setTimeout(()=>tone(f,dur,type||'triangle',peak||0.05),i*gap));
}

window.SFX={
  unlocked:()=>!!ctx,
  unlock(){ ac(); },
  get muted(){ return muted; },
  setMuted(v){
    muted=!!v;
    try{ localStorage.setItem('zjcs_muted',muted?'1':'0'); }catch(e){}
  },
  click(){ tone(760,0.05,'square',0.035,0.85); },
  hover(){ tone(1040,0.03,'sine',0.018); },
  hit(){ noise(0.14,0.075,900); tone(120,0.12,'sawtooth',0.05,0.6); },
  crit(){ noise(0.18,0.09,1500); tone(180,0.16,'sawtooth',0.06,0.5); },
  heal(){ seq([520,700,880],55,0.16,'sine',0.04); },
  chest(){ seq([520,660,790,990],70,0.2,'triangle',0.05); },
  level(){ seq([523,659,784,1046],90,0.28,'triangle',0.06); },
  quest(){ seq([784,988,1174],80,0.3,'sine',0.055); },
  gate(){ tone(300,0.22,'sine',0.05,2.2); setTimeout(()=>tone(640,0.18,'triangle',0.045,1.4),140); },
  fail(){ seq([320,240,180],85,0.25,'sawtooth',0.045); }
};

/* 全局按钮点击音：捕获阶段，避免被各面板的 stopPropagation 吃掉 */
document.addEventListener('click',e=>{
  const el=e.target&&e.target.closest?e.target.closest('button,.nav-badge,.qt-claim'):null;
  if(el)window.SFX.click();
},true);
})();

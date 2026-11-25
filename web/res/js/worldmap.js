/* ============================================================
   杖剑传说 · 6 世界通道地图引擎（v1.1 地形升级）
   ------------------------------------------------------------
   - 每世界 7680×4320（v1.8 分辨率折半：原 15360 的 8× 像素块降到 4×，比例回到
     "路宽≈2 个角色高"，视口 1280×720 不变），视口 1280×720 不变
   - 地形用 game_assets/maps 的 Wn_full_half.png（15360 原图 NEAREST 折半）
   - 移动边界读同源通行掩码（Wn_mask_half.png，白=可走/黑=墙），
     角色被硬约束在通道内——墙是硬碰撞，不是装饰
   - 移速 960px/s（×4），横穿仍约16s；主通道384px=角色显示128px×3
   - 固定点位从服务端 /api/spawn 拉取（怪世界共享 / 宝箱玩家独立），
     坐标来自 Wn_stitch_config.json 的语义位置（设计格×32）
   - 角色/怪物/宝箱用 PartActor 部件动画渲染（v3.0）
   ============================================================ */
(function(){
'use strict';
const MW=7680, MH=4320, VW=1280, VH=720;
const MASK_W=960, MASK_H=540, MASK_SCALE=8; // 掩码降采样比例（7680/960）
const MINI_W=480, MINI_H=270, MINI_SCALE=MW/MINI_W; // 小地图兜底底图（掩码上色）：1px = 16 世界单位
const THUMB_W=960, THUMB_H=540, THUMB_SCALE=MW/THUMB_W; // 小地图主底图（地形缩略）：1px = 8 世界单位
const MOVE_SPEED=480; // 场景像素/秒（v1.8：世界折半，速度同比折半保持手感）
// 安全区出生点：W1 主通道上的开阔点（旧的 7680,4320 落在地形中央蓝色圆内，视觉为水面）
const SPAWN_X=2560, SPAWN_Y=2176;
const RES='/res/';

// 6 世界主题配置（墙元素 / 地面配色 / 障碍绘制风格）
const THEMES={
  grassland:{name:'青草平原',ground:['#79ad4a','#7fb350','#74a746','#85b755'],road:'#c9bb96',wall:'stone',pond:'#3f7fa8',deco:'flower'},
  forest:   {name:'迷雾森林',ground:['#4c7a42','#527f47','#467238','#598a4d'],road:'#9b8f72',wall:'tree',pond:'#356b86',deco:'mushroom'},
  desert:   {name:'赤焰峡谷',ground:['#b9864c','#c29155','#ad7d44','#c99a5e'],road:'#d8c093',wall:'cliff',pond:'#8a5a3c',deco:'crystal'},
  snow:     {name:'冰霜高原',ground:['#c8dae6','#d4e4ee','#bdd2e0','#dcebf3'],road:'#e8f0f6',wall:'ice',pond:'#7fb4d4',deco:'snow'},
  cave:     {name:'流沙遗迹',ground:['#8a7a63','#94836b','#7f705a','#9b8b72'],road:'#b3a181',wall:'dune',pond:'#6b5d49',deco:'ruin'},
  arena:    {name:'星陨之巅',ground:['#4a4560','#514b68','#423d58','#564f6e'],road:'#8a86a3',wall:'meteor',pond:'#3a3550',deco:'star'}
};
const MON_NAMES={slime:'史莱姆',skeleton:'骷髅兵',bat:'蝙蝠',boss_beetle:'甲虫将军'};

function WMap(canvas, opts){
  this.cv=canvas;
  this.ctx=canvas.getContext('2d');
  this.ctx.imageSmoothingEnabled=false;
  this.opts=opts||{};
  this.onEncounter=this.opts.onEncounter||function(){};
  this.onChest=this.opts.onChest||function(){};
  this.onGate=this.opts.onGate||function(){};
  this.onTile=this.opts.onTile||function(){};

  this.hero={x:SPAWN_X,y:SPAWN_Y,dir:'down',moving:false};
  this.keys={};
  this.cam={x:0,y:0};
  this.worldId='W1';
  this.theme=THEMES.grassland;
  this.themeKey='grassland';

  this.obstacles=[];  // 保留字段兼容，地形升级后不再用精灵墙，墙由掩码判定
  this.monsters=[];   // {x,y,nodeId,type,name,state:'idle'|'dead',r,alert,actor,phase}
  this.chests=[];     // {x,y,nodeId,tier,state:'closed'|'opening'|'opened',actor,phase}
  this.gates=[];      // {x,y,nodeId,to,requireLevel,label}
  this.anchors=[];
  this.drops=[];
  this.dust=[];
  this._dustTs=0;
  this.visited=new Set();
  this.remote={};     // 多人：clientId -> {x,y,name,actor}

  this.ground=null;     // 真实地形全图 Image（仅 createImageBitmap 不可用时的兜底分块源）
  this.groundBmp=null;  // 真实地形全图 ImageBitmap（首选：解码/裁剪走解码线程，不占主线程）
  this.maskData=null;   // 通行掩码 ImageData（降采样）
  this.maskCv=null;     // 不可通行区域遮罩（盖住地形图的棋盘格底，勾出可走通道）
  this.paintCv=null;    // 可走区补绘（棋盘格底纹替换为主题地面色）
  this.tiles=null;      // 当前世界分块纹理 Map："tx,ty" -> ImageBitmap/canvas(1280×720)
  this.deco=null;       // 地表装饰层（v1.8：草/花/石/灌木/林缘树，按块烘焙）
  this._decoPending=null;
  this.pal=null;        // 地形自身采样出的配色（补绘/森林纹理取色，保证无缝）
  this._pending=null;   // 异步建块去重 Set
  this._pwToken=0;      // 世界任务令牌：切世界后作废旧异步建块
  this.mapCache={};     // worldId -> {maskData}（只存小掩码；全图不跨世界缓存）

  this.time=0; this.lastTs=0; this.ready=false; this.active=false;
  this.PartActor=window.PartActor;
  this.actorCache={}; // bundle -> PartActor 实例（怪物/宝箱每类一个）
}

/* ---------- 加载真实地形全图 + 通行掩码（异步） ---------- */
// 内存策略：全图解码后 ≈500MB/世界，不跨世界缓存——mapCache 只存小体积
// 掩码(≈8MB)，全图仅当前世界持有；切换世界即释放旧图，防止多世界累积
// 3GB+ 内存引发 GC 卡顿。重访旧世界走浏览器 HTTP 缓存，重载很快。
WMap.prototype.loadTerrain=async function(worldId){
  if(this.mapCache[worldId]){
    this.maskData=this.mapCache[worldId].maskData;
  } else {
    const base=RES+'maps/'+worldId+'_';
    const loadImg=(src)=>new Promise((res,rej)=>{const im=new Image();im.onload=()=>res(im);im.onerror=()=>rej(new Error('地形加载失败:'+src));im.src=src;});
    const maskImg=await loadImg(base+'mask_half.png?v=1.9.4');
    const mc=document.createElement('canvas');mc.width=MASK_W;mc.height=MASK_H;
    const mg=mc.getContext('2d',{willReadFrequently:true});mg.drawImage(maskImg,0,0);
    this.maskData=mg.getImageData(0,0,MASK_W,MASK_H).data;
    this.mapCache[worldId]={maskData:this.maskData};
  }
  // 作废旧世界的异步建块任务并释放旧分块位图
  // 注意：先置空 tiles 再关位图——后续 await 期间渲染会走底色兜底，
  // 否则渲染会取到已关闭(detached)的位图导致 drawImage 报错
  this._pwToken++;
  if(this.thumb&&this.thumb.close){try{this.thumb.close();}catch(e){}} this.thumb=null; this.miniCv=null; this.maskCv=null; this.paintCv=null;
  if(this.deco)this.deco.clear();
  this.waterCv=null;
  this.deco=null; this._decoPending=null; this.pal=null;
  const oldTiles=this.tiles;
  this.tiles=null;this._pending=null;
  if(oldTiles)for(const t of oldTiles.values()){if(t.close)t.close();}
  if(this.groundBmp&&this.groundBmp.close){try{this.groundBmp.close();}catch(e){}}
  this.groundBmp=null;this.ground=null;
  // 全图：仅当前世界持有。优先 fetch→createImageBitmap：
  // 解码与后续按块裁剪均在浏览器解码线程完成，主线程零大图采样（消除预热期掉帧）
  const url=RES+'maps/'+worldId+'_full_half.webp?v=1.9.4';
  try{
    const resp=await fetch(url,{cache:'force-cache'});
    const blob=await resp.blob();
    this.groundBmp=await createImageBitmap(blob);
  }catch(e){
    // 兜底：Image 元素 + 主线程分块（老浏览器/worker 受限时）
    this.ground=await new Promise((res,rej)=>{const im=new Image();im.onload=()=>res(im);im.onerror=()=>rej(new Error('地形加载失败:'+url));im.src=url;});
  }
  this.tiles=new Map();      // 分块缓存只属于当前世界
  this._pending=new Set();
  // 小地图底图（优先级 1）：整世界缩略 1920×1080（1px = 8 世界单位），
  // 与主画面同一张地形图 → 小地图所见即画面所见，位置必然对应
  try{
    const src=this.groundBmp||this.ground;
    this.thumb=await createImageBitmap(src,{resizeWidth:THUMB_W,resizeHeight:THUMB_H,resizeQuality:'low'});
  }catch(e){ this.thumb=null; }
  // 水域修正：地形图里的蓝色水面在掩码中被误标为可走 → 按颜色改为不可走，
  // 让"湖"在视觉与碰撞上一致（角色被挡在岸边，不会站到水面上）
  this.waterFlag=null;
  if(this.thumb&&this.maskData){
    const W=MASK_W,H=MASK_H;
    const tc=document.createElement('canvas');tc.width=W;tc.height=H;
    const tg=tc.getContext('2d',{willReadFrequently:true});
    tg.drawImage(this.thumb,0,0,W,H);
    const px=tg.getImageData(0,0,W,H).data;
    const md=this.maskData, wf=new Uint8Array(W*H);
    let wn=0;
    for(let i=0,p=0;i<px.length;i+=4,p++){
      const r=px[i],g=px[i+1],b=px[i+2];
      if(b>r+45&&b>85&&b>=g-10){
        wf[p]=1;
        if(md[i]>127){md[i]=0;md[i+1]=0;md[i+2]=0;md[i+3]=255;wn++;}
      }
    }
    this.waterFlag=wf; this.waterPixels=wn;
  }
  // 底图（兜底）：缩略失败时用通行掩码上色，保证小地图仍可用
  this.miniCv=this.thumb?null:this.buildMiniMap();
  // 取色：直接从地形图自身采样「草地色 / 林地色」，补绘与遮罩用它作基色，
  // 与真实地形同色系 → 交界不再出现浅米色/深绿硬块
  this.samplePalette();
  // 地表装饰层（草簇/花/石子/灌木/林缘树）：按块惰性烘焙，1:1 像素细节
  this.deco=new window.DecoLayer(this.worldId,this.themeKey);
  this._decoPending=new Set();
  // 水面重着色：原图的水色逐世界不一致（W1 偏紫、W3 偏脏），统一为主题湖水色 + 波纹
  this.waterCv=this.buildWaterLayer();
  // 场景遮罩：不可通行区域 → 林地（真实地形色 + 树冠斑驳纹理）
  this.maskCv=this.buildMaskLayer();
  // 补绘层：未绘制的棋盘格底 → 与地形同色的草地（含颗粒噪点）
  this.paintCv=this.buildPaintLayer();
};

/* 未绘制底检测：地形图未绘制处是"纯色棋盘格"，而 AI 绘制的地表每像素都有噪点。
   v1.9.3：判据由「3×3 完全同色」放宽为「3×3 每通道极差 ≤ 5」——原判据对带轻微
   压缩噪声/渐变的棋盘块会漏检（W1 右侧、W4 雪原均出现漏检的整片米色底），
   漏检块既不会被补绘、也不受掩码遮盖，在大世界里就是硬边直角的"沙地怪块"。
   水面（蓝通道显著高于红）不参与，避免把湖水当纯色底补掉。 */
WMap.prototype.detectSolid=function(src,W,H){
  const N=W*H, solid=new Uint8Array(N);
  for(let y=1;y<H-1;y++){
    const row=y*W;
    for(let x=1;x<W-1;x++){
      const p=row+x, i=p*4;
      const r=src[i],g=src[i+1],b=src[i+2];
      if(b>r+45&&b>85)continue;                       // 水不参与
      let rmin=255,rmax=0,gmin=255,gmax=0,bmin=255,bmax=0;
      for(let dy=-1;dy<=1;dy++){
        const q=(p+dy*W)*4-4;
        for(let dx=0;dx<3;dx++){
          const j=q+dx*4;
          const rr=src[j],gg=src[j+1],bb=src[j+2];
          if(rr<rmin)rmin=rr; if(rr>rmax)rmax=rr;
          if(gg<gmin)gmin=gg; if(gg>gmax)gmax=gg;
          if(bb<bmin)bmin=bb; if(bb>bmax)bmax=bb;
        }
      }
      if(rmax-rmin<=5&&gmax-gmin<=5&&bmax-bmin<=5) solid[p]=1;
    }
  }
  return solid;
};

/* 从地形缩略图采样配色：可走非棋盘区 → 草地均色；不可走非水区 → 林地均色 */
WMap.prototype.samplePalette=function(){
  const pal={ground:[121,173,74],forest:[32,54,34]};
  this.pal=pal;
  if(!this.thumb||!this.maskData)return pal;
  const W=MASK_W,H=MASK_H;
  const tc=document.createElement('canvas');tc.width=W;tc.height=H;
  const tg=tc.getContext('2d',{willReadFrequently:true});
  tg.drawImage(this.thumb,0,0,W,H);
  const src=tg.getImageData(0,0,W,H).data;
  const md=this.maskData;
  const solid=this.solidMask=this.detectSolid(src,W,H);
  let gr=0,gg=0,gb=0,gn=0, fr=0,fg=0,fb=0,fn=0;
  for(let i=0,p=0;i<src.length;i+=4,p++){
    const r=src[i],g=src[i+1],b=src[i+2];
    if(md[i]>127){                       // 可走
      if(solid[p])continue;              // 未绘制的纯色棋盘底不参与取色
      gr+=r;gg+=g;gb+=b;gn++;
    }else{                               // 不可走
      if(b>r+45&&b>85)continue;          // 水不参与
      fr+=r;fg+=g;fb+=b;fn++;
    }
  }
  if(gn>500){pal.ground=[Math.round(gr/gn),Math.round(gg/gn),Math.round(gb/gn)];}
  if(fn>500){pal.forest=[Math.round(fr/fn),Math.round(fg/fn),Math.round(fb/fn)];}
  // 铺装面（石板广场 / 雪地等低饱和亮面）：装饰层据此跳过草丛与花，避免"砖缝里长草"
  const pv=new Uint8Array(W*H);
  for(let q=0;q<W*H;q++){
    const i=q*4, r=src[i],g=src[i+1],b=src[i+2];
    const mx=r>g?(r>b?r:b):(g>b?g:b), mn=r<g?(r<b?r:b):(g<b?g:b);
    pv[q]=(mx-mn<=26&&(r+g+b)/3>=118)?1:0;
  }
  this.pavedMask=pv;
  return pal;
};

/* 可走区补绘：把"未绘制的纯色棋盘底"就地补成周围地形的颜色（简易 inpainting）
   —— 不依赖固定色值，也不依赖主题配色：把已知像素向外扩散 8 轮，
   补出的颜色天然与周边一致，因此接缝几乎不可见；再叠细颗粒避免死板 */
WMap.prototype.buildPaintLayer=function(){
  if(!this.thumb||!this.maskData)return null;
  const W=MASK_W,H=MASK_H,N=W*H;
  const tc=document.createElement('canvas');tc.width=W;tc.height=H;
  const tg=tc.getContext('2d',{willReadFrequently:true});
  tg.drawImage(this.thumb,0,0,W,H);
  const src=tg.getImageData(0,0,W,H).data;
  const md=this.maskData;
  // 未绘制底 = 纯色棋盘格（3×3 全同色），逐世界自适应
  const solid=this.solidMask||this.detectSolid(src,W,H);
  // 膨胀 1 圈：棋盘格的"格缝"像素本身不是纯色，需一并纳入补绘，否则留下网格线
  const hit=new Uint8Array(N);
  let raw=0;
  for(let y=1;y<H-1;y++){
    const row=y*W;
    for(let x=1;x<W-1;x++){
      const p=row+x;
      if(!solid[p]||!(md[p*4]>127))continue;
      hit[p]=1;hit[p-1]=1;hit[p+1]=1;hit[p-W]=1;hit[p+W]=1;raw++;
    }
  }
  if(!raw)return null;
  // 颜色传播：known=1 的像素有颜色，其余从 8 邻域已知像素取平均，迭代 10 轮
  const R=new Float32Array(N),G=new Float32Array(N),B=new Float32Array(N),known=new Uint8Array(N);
  for(let p=0;p<N;p++){
    const i=p*4;
    if(!hit[p]){ R[p]=src[i];G[p]=src[i+1];B[p]=src[i+2];known[p]=1; }
  }
  for(let it=0;it<10;it++){
    let changed=0;
    for(let y=1;y<H-1;y++){
      const row=y*W;
      for(let x=1;x<W-1;x++){
        const p=row+x;
        if(known[p])continue;
        let r=0,g=0,b=0,n=0;
        for(let dy=-1;dy<=1;dy++)for(let dx=-1;dx<=1;dx++){
          const q=p+dy*W+dx;
          if(known[q]){r+=R[q];g+=G[q];b+=B[q];n++;}
        }
        if(n){ R[p]=r/n;G[p]=g/n;B[p]=b/n;known[p]=1;changed++; }
      }
    }
    if(!changed)break;
  }
  const pc=(this.pal&&this.pal.ground)||[121,173,74];
  const out=new ImageData(W,H), d=out.data;
  let hits=0;
  for(let p=0;p<N;p++){
    const i=p*4;
    if(!hit[p]){ d[i+3]=0; continue; }
    const x=p%W, y=(p/W)|0;
    const n1=((x*13+y*7)%11)-5, n2=(((x>>2)*29+(y>>2)*17)%9)-4;
    const n=n1*0.6+n2*0.8;
    const cr=known[p]?R[p]:pc[0], cg=known[p]?G[p]:pc[1], cb=known[p]?B[p]:pc[2];
    const shade=((x*31+y*17)%23)<3?-6:0;         // 稀疏暗斑（踩踏痕迹），避免死板纯色
    d[i]=Math.max(0,Math.min(255,cr+n+shade));
    d[i+1]=Math.max(0,Math.min(255,cg+n+shade));
    d[i+2]=Math.max(0,Math.min(255,cb+n+shade));
    d[i+3]=255;   // 完全不透明：252 会让下层原图的碎石噪点透出来
    hits++;
  }
  this.paintHits=hits; this.paintRaw=raw;
  const ctx2=tc.getContext('2d');
  ctx2.putImageData(out,0,0);
  return tc;
};

/* 水面层：把原图的"水"统一重着色为主题湖水色，并叠波纹与岸线
   —— 原图各世界水色不一致（W1 偏紫、W3 偏脏、W6 偏黑），直接露出很违和 */
WMap.prototype.buildWaterLayer=function(){
  const wf=this.waterFlag;
  if(!wf)return null;
  const W=MASK_W,H=MASK_H;
  const hexMap={grassland:'#3f7fa8',forest:'#35657f',desert:'#6f4f38',snow:'#7fb4d4',cave:'#5c523f',arena:'#33304d'};
  const hex=hexMap[this.themeKey]||'#3f7fa8';
  const cr=parseInt(hex.slice(1,3),16),cg=parseInt(hex.slice(3,5),16),cb=parseInt(hex.slice(5,7),16);
  const cv=document.createElement('canvas');cv.width=W;cv.height=H;
  const g=cv.getContext('2d');
  const img=g.createImageData(W,H), d=img.data;
  let n=0;
  for(let p=0;p<W*H;p++){
    if(!wf[p]){ d[p*4+3]=0; continue; }
    const x=p%W,y=(p/W)|0;
    // 近岸浅、中心深：以 6px 邻域的水像素密度近似"离岸距离"
    let land=0;
    for(let dy=-3;dy<=3;dy+=3)for(let dx=-3;dx<=3;dx+=3){
      const q=(y+dy)*W+(x+dx);
      if(q>=0&&q<W*H&&!wf[q])land++;
    }
    const shallow=land>=2;
    const t=((x*7+y*13)%5)-2;
    d[p*4]  =Math.max(0,Math.min(255,cr+(shallow?26:0)+t));
    d[p*4+1]=Math.max(0,Math.min(255,cg+(shallow?24:0)+t));
    d[p*4+2]=Math.max(0,Math.min(255,cb+(shallow?18:0)+t));
    d[p*4+3]=255;
    n++;
  }
  g.putImageData(img,0,0);
  // 波纹：按掩码裁剪的横线
  g.globalCompositeOperation='source-atop';
  g.strokeStyle='rgba(255,255,255,.16)';
  g.lineWidth=1.2;
  for(let y=6;y<H;y+=11){
    g.beginPath();
    for(let x=0;x<W;x+=8)g.lineTo(x,y+Math.sin(x*0.35+y)*1.2);
    g.stroke();
  }
  g.globalCompositeOperation='source-over';
  this.waterPixels=n;
  return cv;
};

/* 由 maskData 生成不可通行区域遮罩（与掩码同尺寸 1920×1080，放大 8× 绘制）
   v1.8：不再是纯色深绿，而是「地形自身林色 + 多层树冠斑驳」——远看是森林，
   边缘由装饰层的灌木/树冠进一步柔化，与可走草地自然咬合 */
WMap.prototype.buildMaskLayer=function(){
  const src=this.maskData;
  if(!src)return null;
  const W=MASK_W,H=MASK_H;
  // 1) 形状层：可走=透明，不可走=不透明（水保持透明，露出湖水原色）
  const sc=document.createElement('canvas');sc.width=W;sc.height=H;
  const sg=sc.getContext('2d');
  const so=sg.createImageData(W,H), sd=so.data;
  const wf=this.waterFlag;
  for(let y=0;y<H;y++){
    const row=y*W;
    for(let x=0;x<W;x++){
      const i=(row+x)*4;
      if(src[i]>127){ sd[i+3]=0; continue; }
      if(wf&&wf[row+x]){ sd[i+3]=0; continue; }
      sd[i]=255; sd[i+1]=255; sd[i+2]=255; sd[i+3]=255;
    }
  }
  sg.putImageData(so,0,0);
  // 2) 纹理层：林地底色 + 低频明暗（树冠间隙的林下光斑），按形状层裁剪
  const cv=document.createElement('canvas');cv.width=W;cv.height=H;
  const cg=cv.getContext('2d');
  const pc=(this.pal&&this.pal.forest)||[32,54,34];
  cg.fillStyle=`rgb(${pc[0]},${pc[1]},${pc[2]})`;
  cg.fillRect(0,0,W,H);
  const img=cg.getImageData(0,0,W,H), d=img.data;
  for(let y=0;y<H;y++){
    for(let x=0;x<W;x++){
      const i=(y*W+x)*4;
      // 低频噪声：4px 网格 → 放大 8× 后是 32px 的柔和林冠明暗
      const hx=x>>2, hy=y>>2;
      let v=((hx*374761393+hy*668265263)^(hx*hy*69069))>>>0;
      v=(v^(v>>>13))>>>0;
      const n=((v%13)-6);
      d[i]=Math.max(0,pc[0]+n);
      d[i+1]=Math.max(0,pc[1]+n+1);
      d[i+2]=Math.max(0,pc[2]+n);
      d[i+3]=255;
    }
  }
  cg.putImageData(img,0,0);
  cg.globalCompositeOperation='destination-in';
  cg.drawImage(sc,0,0);
  cg.globalCompositeOperation='source-over';
  return cv;
};

/* 由 maskData 生成小地图底图（960×540，1px = 16 世界单位），2×2 采样平均 */
WMap.prototype.buildMiniMap=function(){
  const src=this.maskData;
  if(!src)return null;
  const W=960,H=540,step=MASK_W/W; // =2
  const cv=document.createElement('canvas');cv.width=W;cv.height=H;
  const cg=cv.getContext('2d');
  const out=cg.createImageData(W,H), dst=out.data;
  for(let y=0;y<H;y++){
    for(let x=0;x<W;x++){
      let walk=0;
      for(let sy=0;sy<step;sy++)for(let sx=0;sx<step;sx++){
        const px=Math.min(MASK_W-1,x*step+sx), py=Math.min(MASK_H-1,y*step+sy);
        if(src[((py*MASK_W)+px)*4]>127)walk++;
      }
      const r=walk/(step*step);           // 可走占比 → 五档明暗，边缘自然
      const j=(y*W+x)*4;
      if(r>0.5){
        const t=Math.round(r*60);         // 0..60 提亮
        dst[j]=84+t; dst[j+1]=132+t; dst[j+2]=64+Math.round(t*0.4);
      }else{
        const t=Math.round(r*40);
        dst[j]=26+t; dst[j+1]=40+t; dst[j+2]=26+Math.round(t*0.6);
      }
      dst[j+3]=255;
    }
  }
  cg.putImageData(out,0,0);
  return cv;
};

/* 分块纹理：异步创建（createImageBitmap 走解码线程）+ LRU（上限36块≈133MB） */
WMap.prototype.TILE_MAX=24;
WMap.prototype.requestTile=function(tx,ty){ // 请求异步建块（去重），不阻塞渲染
  if(!this.tiles)return;
  if(tx<0||ty<0||tx>=MW/VW||ty>=MH/VH)return;
  const key=tx+','+ty;
  if(this.tiles.has(key)||this._pending.has(key))return;
  this._pending.add(key);
  const token=this._pwToken;
  const done=(bmp)=>{
    if(token!==this._pwToken||!this.tiles){ if(bmp&&bmp.close)bmp.close(); return; } // 世界已切/加载中，丢弃
    this._pending.delete(key);
    if(bmp){
      this.tiles.set(key,bmp);
      while(this.tiles.size>this.TILE_MAX){ // LRU 淘汰最旧块并释放位图
        const oldest=this.tiles.keys().next().value;
        const ob=this.tiles.get(oldest);
        if(ob&&ob.close)ob.close();
        this.tiles.delete(oldest);
      }
    }
  };
  if(this.groundBmp){
    createImageBitmap(this.groundBmp,tx*VW,ty*VH,VW,VH).then(done).catch(()=>done(null));
  } else if(this.ground){
    Promise.resolve().then(()=>{
      const c=document.createElement('canvas');c.width=VW;c.height=VH;
      const g=c.getContext('2d');g.imageSmoothingEnabled=false;
      g.drawImage(this.ground,tx*VW,ty*VH,VW,VH,0,0,VW,VH);
      done(c);
    });
  } else { this._pending.delete(key); }
};
WMap.prototype.ensureTile=function(tx,ty){ // 渲染取块：命中返回纹理，未命中触发异步建块
  if(!this.tiles)return null;
  const key=tx+','+ty;
  let tile=this.tiles.get(key);
  if(tile){ // LRU 刷新：移到末尾
    this.tiles.delete(key);this.tiles.set(key,tile);
    return tile;
  }
  this.requestTile(tx,ty);
  return null;
};

/* 装饰块：首次访问异步烘焙（下一帧就绪），避免同步生成卡住当前帧 */
WMap.prototype.ensureDeco=function(tx,ty){
  if(!this.deco)return null;
  if(tx<0||ty<0||tx>=MW/VW||ty>=MH/VH)return null;
  const key=tx+','+ty;
  const hit=this.deco.tiles.get(key);
  if(hit){ this.deco.tiles.delete(key);this.deco.tiles.set(key,hit); return hit; }
  if(this._decoPending.has(key))return null;
  this._decoPending.add(key);
  const token=this._pwToken, self=this;
  const job=()=>{
    self._decoPending.delete(key);
    if(token!==self._pwToken||!self.deco)return;
    const walk=(x,y)=>self.isWalkable(x,y);
    const isW=(x,y)=>self.isWaterAt(x,y);
    const isPv=(x,y)=>self.isPavedAt(x,y);
    self.deco.get(tx,ty,VW,VH,walk,isW,isPv);
  };
  if(window.requestIdleCallback)requestIdleCallback(job,{timeout:120});
  else setTimeout(job,16);
  return null;
};

/* ---------- 初始化（只建一次角色 actor） ---------- */
WMap.prototype.load=async function(){
  const self=this;
  // 预载角色部件 actor
  if(!this.heroActor){
    this.heroActor=new this.PartActor('char_hero_mage');
    await this.heroActor.init();
    this.heroActor.play('idle');
  }
  await this.loadTerrain(this.worldId);
  this.centerCam();
  this.ready=true;
  if(!this._inputBound){this.bindInput();this._inputBound=true;}
};

/* ---------- 切换世界 + 接入服务端点位 ---------- */
WMap.prototype.switchWorld=async function(worldId, spots, heroX, heroY){
  this.worldId=worldId;
  const wdef=(this.opts.worldDefs||[]).find(w=>w.id===worldId);
  const themeKey=wdef?wdef.theme:'grassland';
  this.themeKey=themeKey;
  this.theme=THEMES[themeKey]||THEMES.grassland;
  await this.loadTerrain(worldId);
  // 重置实体
  this.monsters=[];this.chests=[];this.gates=[];this.anchors=[];this.drops=[];
  if(heroX!=null&&heroY!=null){this.hero.x=heroX;this.hero.y=heroY;}
  else {this.hero.x=SPAWN_X;this.hero.y=SPAWN_Y;} // 默认落安全区
  // 若存档坐标落在墙里（旧存档），修正到安全区
  if(this.blocked(this.hero.x,this.hero.y,8)){this.hero.x=SPAWN_X;this.hero.y=SPAWN_Y;}
  this.applySpots(spots||[]);
  this.centerCam();
  this.prewarmTiles(); // 后台预热分块，避免移动跨块时同步建块掉帧
};

/* 分块预热：从角色所在块螺旋向外全部异步请求（createImageBitmap 走解码线程，
   主线程零开销，不再与渲染争抢）；未预热完的帧由主题底色兜底 */
WMap.prototype.prewarmTiles=function(){
  if(!this.tiles)return;
  const cols=MW/VW, rows=MH/VH;
  const hc=Math.floor(this.hero.x/VW), hr=Math.floor(this.hero.y/VH);
  const order=[];
  for(let r=0;r<Math.max(cols,rows);r++)
    for(let ty=hr-r;ty<=hr+r;ty++)for(let tx=hc-r;tx<=hc+r;tx++)
      if(tx>=0&&ty>=0&&tx<cols&&ty<rows&&Math.max(Math.abs(tx-hc),Math.abs(ty-hr))===r)order.push([tx,ty]);
  for(const [tx,ty] of order)this.requestTile(tx,ty);
};

/* 用服务端点位生成实体（固定坐标 + 随机内容） */
WMap.prototype.applySpots=function(spots){
  const self=this;
  for(const s of spots){
    if(s.type==='ANCHOR'){this.anchors.push(s);continue;}
    if(s.type==='GATE'){
      this.gates.push({x:s.x,y:s.y,nodeId:s.nodeId,to:s.to,requireLevel:s.reqLevel||s.requireLevel||0,label:'通往 '+(s.to||'?')});
      continue;
    }
    if(s.type==='MOB'||s.type==='ELITE'||s.type==='BOSS'){
      const monId=s.contentId||s.bossMonster||'slime';
      const sprite=MON_NAMES[monId]?monId:'slime';
      const actor=this.makeFreshActor('mon_'+sprite,'idle');
      this.monsters.push({x:s.x,y:s.y,nodeId:s.nodeId,type:sprite,name:(s.type==='ELITE'?'精英 ':s.type==='BOSS'?'首领 ':'')+(MON_NAMES[sprite]||monId),
        state:'idle',r:30,alert:s.type==='ELITE'?360:300,actor,phase:Math.random()*6,
        monId, elite:s.type!=='MOB'});
      continue;
    }
    if(s.type==='CHEST'){
      const tier=['wood','silver','gold'][Math.max(0,Math.min(2,(s.tier||1)-1))]||'wood';
      const actor=this.makeFreshActor('item_chest_'+tier,'closed');
      this.chests.push({x:s.x,y:s.y,nodeId:s.nodeId,tier,state:'closed',actor,phase:Math.random()*6});
    }
  }
};

WMap.prototype.getActor=function(bundle){
  if(this.actorCache[bundle])return this.actorCache[bundle];
  // 部件 actor 需异步 init；这里先建，init 完成前 render 会跳过
  const a=new this.PartActor(bundle);
  a.init().then(()=>a.play(a.state||'idle'));
  this.actorCache[bundle]=a;
  return a;
};
// 独立 actor：资源仍走 PartActor 全局缓存（不重复下载），但 state/t0 独立，
// 避免开箱动画污染同类其它宝箱（旧实现按 bundle 共享导致新箱显示"已开启"）。
WMap.prototype.makeFreshActor=function(bundle,state){
  const a=new this.PartActor(bundle);
  a.init().then(()=>{ if(a.state==='idle'&&state)a.play(state); });
  if(state)a.state=state;
  return a;
};

/* ---------- 地图底层生成（离屏 canvas，按主题） ---------- */
WMap.prototype.buildMap=async function(){
  const off=document.createElement('canvas');
  off.width=MW; off.height=MH;
  const g=off.getContext('2d');
  g.imageSmoothingEnabled=false;
  const th=this.theme;
  let seed=1337;
  const rnd=()=>{seed=(seed*9301+49297)%233280;return seed/233280;};

  // 基础地面
  g.fillStyle=th.ground[0];g.fillRect(0,0,MW,MH);
  for(let y=0;y<MH;y+=80)for(let x=0;x<MW;x+=80){
    g.fillStyle=th.ground[Math.floor(rnd()*th.ground.length)];
    g.fillRect(x,y,80,80);
  }
  this.drawDeco(g,rnd,th);

  // 通道网络：放射主道 + 环形支路 + 斜向捷径（地面用 road 色）
  const road=(pts,w)=>{
    g.strokeStyle=th.road;g.lineWidth=w;g.lineCap='round';g.lineJoin='round';
    g.beginPath();g.moveTo(pts[0][0],pts[0][1]);
    for(let i=1;i<pts.length;i++)g.lineTo(pts[i][0],pts[i][1]);g.stroke();
    g.strokeStyle='rgba(255,255,255,.10)';g.lineWidth=w-12;g.stroke();
    g.fillStyle='rgba(60,50,30,.18)';
    for(let i=1;i<pts.length;i++){
      const [ax,ay]=pts[i-1],[bx,by]=pts[i];
      const len=Math.hypot(bx-ax,by-ay),n=Math.floor(len/48);
      for(let k=0;k<n;k++){const t=k/n,sx=ax+(bx-ax)*t,sy=ay+(by-ay)*t;g.fillRect(sx-w/2+5,sy-2,12,4);}
    }
  };
  const cx=MW/2,cy=MH/2;
  road([[0,cy],[MW,cy]],150);                 // 横贯主道
  road([[cx,0],[cx,MH]],150);                 // 纵贯主道
  road([[0,cy],[cx*0.6,MH*0.85],[cx,cy],[cx*1.45,MH*0.2],[MW,cy]],110); // 斜向支路
  road([[cx,0],[MW*0.82,cy*0.7],[cx,MH],[MW*0.2,cy*0.7],[cx,0]],96);    // 环形支路

  // 水域 / 主题地形点缀（按主题）
  if(th.pond){this.drawPond(g,cx*0.3,cy*0.35,170,th.pond);this.drawPond(g,cx*1.7,cy*1.6,200,th.pond);}

  this.ground=off;
  this.buildObstacles(rnd,th,cx,cy);
};

WMap.prototype.drawDeco=function(g,rnd,th){
  const colors=th.deco==='flower'?['#f5f0d8','#f0d590','#e8a0b8']:
               th.deco==='crystal'?['#e08a5a','#f0b070','#c85a3a']:
               th.deco==='star'?['#d8d0ff','#f0d590','#9ad8ff']:
               th.deco==='mushroom'?['#d98a8a','#e8b0a0','#f0d590']:
               ['#ffffff','#e8f0f6','#c8dae6'];
  for(let i=0;i<520;i++){
    const x=rnd()*MW,y=rnd()*MH;
    g.fillStyle=colors[Math.floor(rnd()*colors.length)];
    g.fillRect(x,y,4,4);g.fillRect(x-2,y+2,2,2);g.fillRect(x+4,y+2,2,2);
  }
};
WMap.prototype.drawPond=function(g,cx,cy,r,color){
  g.fillStyle=color;g.beginPath();g.ellipse(cx,cy,r,r*0.72,0,0,Math.PI*2);g.fill();
  g.fillStyle='rgba(255,255,255,.18)';g.beginPath();g.ellipse(cx-8,cy-8,r*0.7,r*0.45,0,0,Math.PI*2);g.fill();
};

/* 主题墙：沿通道两侧成排布置，形成"墙"隔断；边界密排成围墙 */
WMap.prototype.buildObstacles=function(rnd,th,cx,cy){
  const O=this.obstacles=[];
  const wall=th.wall, edge=80;
  const push=(x,y,r,kind)=>O.push({x,y,r,kind,v:Math.floor(rnd()*2)});
  // 边界围墙
  for(let x=edge;x<MW-edge;x+=96){push(x,edge,36,wall);push(x,MH-edge,36,wall);}
  for(let y=edge+70;y<MH-edge;y+=96){push(edge,y,36,wall);push(MW-edge,y,36,wall);}
  // 通道隔断：沿主道两侧平行布置"墙带"，留出路口
  const isNearRoad=(x,y)=>{
    if(Math.abs(y-cy)<130)return true;      // 横主道
    if(Math.abs(x-cx)<130)return true;      // 纵主道
    if(Math.abs((y-cy)-0.7*(x-cx))<110)return true; // 斜道
    if(Math.abs((y-cy)+0.7*(x-cx))<110)return true;
    return false;
  };
  const clusters=[[600,600],[2900,600],[600,1600],[2900,1600],[1500,500],[2400,1700],
                  [1200,1600],[2700,1200],[1000,1100],[2200,950],[1700,1500],[3200,1080],
                  [cx-260,cy-240],[cx+280,cy+260]];
  for(const [bx,by] of clusters){
    for(let i=0;i<5;i++){
      const x=bx+(rnd()-0.5)*220, y=by+(rnd()-0.5)*220;
      if(isNearRoad(x,y))continue;
      if(Math.hypot(x-cx,y-cy)<200)continue;
      push(x,y,32,wall);
    }
  }
  this.wallKind=wall;
};

/* ---------- 碰撞：读通行掩码，白(255)=可走，黑(0)=墙 ---------- */
// 检查角色 (x,y) 半径 r 覆盖范围内的采样点是否全在可走区
WMap.prototype.isWaterAt=function(x,y){
  if(!this.waterFlag)return false;
  const mx=Math.floor(x/MASK_SCALE),my=Math.floor(y/MASK_SCALE);
  if(mx<0||my<0||mx>=MASK_W||my>=MASK_H)return false;
  return this.waterFlag[my*MASK_W+mx]===1;
};
WMap.prototype.isPavedAt=function(x,y){
  if(!this.pavedMask)return false;
  const mx=Math.floor(x/MASK_SCALE),my=Math.floor(y/MASK_SCALE);
  if(mx<0||my<0||mx>=MASK_W||my>=MASK_H)return false;
  return this.pavedMask[my*MASK_W+mx]===1;
};
WMap.prototype.isWalkable=function(x,y){
  if(!this.maskData)return true; // 掩码未加载时不阻挡（避免卡死）
  const mx=Math.floor(x/MASK_SCALE),my=Math.floor(y/MASK_SCALE);
  if(mx<0||my<0||mx>=MASK_W||my>=MASK_H)return false;
  return this.maskData[(my*MASK_W+mx)*4]>127; // 取R通道（灰度图RGB相同）
};
WMap.prototype.blocked=function(x,y,r){
  if(!this.maskData)return false;
  // 采样中心 + 上下左右 + 四角（覆盖角色体积），任一落在墙里即阻挡
  const pts=[[0,0],[r,0],[-r,0],[0,r],[0,-r],[r*0.7,r*0.7],[-r*0.7,r*0.7],[r*0.7,-r*0.7],[-r*0.7,-r*0.7]];
  for(const [dx,dy] of pts)if(!this.isWalkable(x+dx,y+dy))return true;
  return false;
};
WMap.prototype.centerCam=function(){
  this.cam.x=Math.max(0,Math.min(MW-VW,this.hero.x-VW/2));
  this.cam.y=Math.max(0,Math.min(MH-VH,this.hero.y-VH/2));
};

/* ---------- 输入 ---------- */
WMap.prototype.bindInput=function(){
  const self=this;
  addEventListener('keydown',e=>{
    if(!self.active)return;
    const k=e.key.toLowerCase();
    if(['w','a','s','d','arrowup','arrowdown','arrowleft','arrowright'].includes(k)){self.keys[k]=true;e.preventDefault();}
  });
  addEventListener('keyup',e=>{self.keys[e.key.toLowerCase()]=false;});
  this.cv.addEventListener('click',e=>{
    if(!self.active)return;
    const rect=this.cv.getBoundingClientRect();
    const sx=(e.clientX-rect.left)/rect.width*VW, sy=(e.clientY-rect.top)/rect.height*VH;
    const wx=sx+self.cam.x, wy=sy+self.cam.y;
    const gate=self.nearestGate(wx,wy,200);
    if(gate){self.opts.onGate&&self.opts.onGate(gate);return;}
    const mon=self.nearestMonster(wx,wy,190);
    if(mon){self.opts.onEncounter&&self.opts.onEncounter(mon);return;}
    const ch=self.nearestChest(wx,wy,190);
    if(ch){self.tryOpenChest(ch);return;}
    // 已移除点击地面移动：仅保留 WASD/方向键移动，点击只用于交互（怪/箱/传送门）
  });
};
WMap.prototype.nearestGate=function(x,y,maxd){let b=null,d0=maxd;for(const g of this.gates){const d=Math.hypot(g.x-x,g.y-y);if(d<d0){d0=d;b=g;}}return b;};
WMap.prototype.nearestMonster=function(x,y,maxd){let b=null,d0=maxd||110;for(const m of this.monsters){if(m.state!=='idle')continue;const d=Math.hypot(m.x-x,m.y-y);if(d<d0){d0=d;b=m;}}return b;};
WMap.prototype.nearestChest=function(x,y,maxd){let b=null,d0=maxd||80;for(const c of this.chests){if(c.state!=='closed')continue;const d=Math.hypot(c.x-x,c.y-y);if(d<d0){d0=d;b=c;}}return b;};

/* ---------- 移动更新 ---------- */
WMap.prototype.update=function(dt){
  if(!this.active)return;
  const h=this.hero,spd=MOVE_SPEED*dt;
  let dx=0,dy=0;
  if(this.keys.w||this.keys.arrowup)dy-=1;
  if(this.keys.s||this.keys.arrowdown)dy+=1;
  if(this.keys.a||this.keys.arrowleft)dx-=1;
  if(this.keys.d||this.keys.arrowright)dx+=1;
  // 仅键盘移动，已移除点击地面/自动寻路
  h.moving=(dx!==0||dy!==0);
  if(h.moving){
    if(h.moving&&this.heroActor&&this.heroActor.state!=='walk')this.heroActor.play('walk');
    if(Math.abs(dx)>Math.abs(dy))h.dir=dx>0?'right':'left';else h.dir=dy>0?'down':'up';
    const nx=h.x+dx*spd,ny=h.y+dy*spd;
    // 掩码精度8px，角色碰撞半径取24（≈显示半宽，保证不穿墙）
    if(!this.blocked(nx,h.y,24))h.x=nx;
    if(!this.blocked(h.x,ny,24))h.y=ny;
    h.x=Math.max(30,Math.min(MW-30,h.x));h.y=Math.max(30,Math.min(MH-30,h.y));
    const tx=Math.floor(h.x/640),ty=Math.floor(h.y/640);
    this.onTile&&this.onTile(tx+','+ty);this.visited.add(tx+','+ty);
  } else if(this.heroActor&&this.heroActor.state==='walk'){this.heroActor.play('idle');}
  // 走路扬尘：每 90ms 在脚后生成一粒，短寿命淡出
  if(h.moving&&this.time-(this._dustTs||0)>0.09){
    this._dustTs=this.time;
    this.dust.push({x:h.x+(Math.random()-0.5)*18,y:h.y+4,vx:(Math.random()-0.5)*14,vy:-14-Math.random()*10,life:0.55,max:0.55,r:2+Math.random()*2.5});
    if(this.dust.length>26)this.dust.shift();
  }
  for(const d of this.dust){ d.x+=d.vx*dt; d.y+=d.vy*dt; d.vy+=26*dt; d.life-=dt; }
  this.dust=this.dust.filter(d=>d.life>0);
  this.centerCam();
  // 掉落物理
  for(const d of this.drops){
    d.vy+=1400*dt;d.y+=d.vy*dt;
    if(d.y>=d.groundY){d.y=d.groundY;if(Math.abs(d.vy)>60)d.vy*=-0.42;else{this.chests.push({x:d.x,y:d.y,nodeId:'drop_'+Date.now(),tier:d.tier,state:'closed',actor:d.actor||this.makeFreshActor('item_chest_'+d.tier,'closed'),phase:0,dropped:true});d.done=true;}}
  }
  this.drops=this.drops.filter(d=>!d.done);
  // 开启动画
  for(const c of this.chests){
    if(c.state==='opening'&&c.actor&&c.actor.state!=='open'){
      c.actor.playOnce('open',()=>{c.state='opened';});
      c.state='opening_done';
    } else if(c.state==='opening_done'&&c.actor&&c.actor.state!=='open'&&c._opener){
      c.state='opened';
    }
  }
};

/* ---------- 交互 ---------- */
WMap.prototype.tryOpenChest=function(c){
  if(c.state!=='closed')return;
  if(Math.hypot(c.x-this.hero.x,c.y-this.hero.y)>260){
    // 已移除点击自动寻路：用键盘（WASD/方向键）走到宝箱附近再点击开启
    if(this.opts.onHint)this.opts.onHint('请用 WASD / 方向键走到宝箱附近再开启');
    return;
  }
  this.openChest(c);
};
WMap.prototype.openChest=function(c){
  if(c.state!=='closed')return;
  c.state='opening';
  if(c.actor)c.actor.playOnce('open',()=>{c.state='opened';});
  this.opts.onChest&&this.opts.onChest(c);
};
WMap.prototype.dropChestFrom=function(mon){
  mon.state='dead';
  if(mon.actor)mon.actor.playOnce('die');
  const tier=mon.type==='boss_beetle'?'gold':(Math.random()<0.5?'silver':'wood');
  this.drops.push({x:mon.x,y:mon.y-90,groundY:mon.y,tier,actor:this.makeFreshActor('item_chest_'+tier,'closed'),vy:-120});
  return true;
};
WMap.prototype.heroDefeated=function(){};

/* ---------- 渲染 ---------- */
WMap.prototype.render=function(now){
  if(!this.ready)return;
  const dt=Math.min(0.05,(now-this.lastTs)/1000||0);this.lastTs=now;this.time+=dt;
  this.update(dt);
  const g=this.ctx,cx=this.cam.x,cy=this.cam.y,t=this.time;
  // 真实地形：分块纹理绘制（每帧仅 2×2 块视口大小的小纹理）。
  // 块未就绪时先铺主题底色（异步建块毫秒级返回，肉眼几乎不可见）
  if(this.tiles){
    g.fillStyle=this.theme.ground[0];
    g.fillRect(0,0,VW,VH);
    const tx0=Math.floor(cx/VW),ty0=Math.floor(cy/VH);
    const tx1=Math.floor((cx+VW-0.001)/VW),ty1=Math.floor((cy+VH-0.001)/VH);
    for(let ty=ty0;ty<=ty1;ty++)for(let tx=tx0;tx<=tx1;tx++){
      const tile=this.ensureTile(tx,ty);
      if(tile)g.drawImage(tile,tx*VW-cx,ty*VH-cy);
    }
  }
  else {g.fillStyle='#3a5a2a';g.fillRect(0,0,VW,VH);}
  // 水面：统一湖水色（盖住原图的脏色），再叠波纹
  if(this.waterCv){
    const ms=MASK_SCALE;
    let sx=cx/ms, sy=cy/ms, sw=VW/ms, sh=VH/ms;
    if(sx<0){sw+=sx;sx=0;}
    if(sy<0){sh+=sy;sy=0;}
    if(sx+sw>MASK_W)sw=MASK_W-sx;
    if(sy+sh>MASK_H)sh=MASK_H-sy;
    if(sw>0&&sh>0)g.drawImage(this.waterCv,sx,sy,sw,sh,sx*ms-cx,sy*ms-cy,sw*ms,sh*ms);
  }
  // 不可通行区域遮罩：盖住地形图的棋盘格/透明底，只留出可走通道（与碰撞一致）
  if(this.maskCv){
    const ms=MASK_SCALE;
    let sx=cx/ms, sy=cy/ms, sw=VW/ms, sh=VH/ms;
    if(sx<0){sw+=sx;sx=0;}
    if(sy<0){sh+=sy;sy=0;}
    if(sx+sw>MASK_W)sw=MASK_W-sx;
    if(sy+sh>MASK_H)sh=MASK_H-sy;
    if(sw>0&&sh>0)g.drawImage(this.maskCv,sx,sy,sw,sh,sx*ms-cx,sy*ms-cy,sw*ms,sh*ms);
  }
  // 可走区补绘（棋盘格底纹 → 主题地面色）
  if(this.paintCv){
    const ms=MASK_SCALE;
    let sx=cx/ms, sy=cy/ms, sw=VW/ms, sh=VH/ms;
    if(sx<0){sw+=sx;sx=0;}
    if(sy<0){sh+=sy;sy=0;}
    if(sx+sw>MASK_W)sw=MASK_W-sx;
    if(sy+sh>MASK_H)sh=MASK_H-sy;
    if(sw>0&&sh>0)g.drawImage(this.paintCv,sx,sy,sw,sh,sx*ms-cx,sy*ms-cy,sw*ms,sh*ms);
  }
  // 地表装饰（草簇/花/石子/灌木/林缘树）：按块 1:1 烘焙，异步建块不阻塞渲染
  if(this.deco){
    const dx0=Math.floor(cx/VW),dy0=Math.floor(cy/VH);
    const dx1=Math.floor((cx+VW-0.001)/VW),dy1=Math.floor((cy+VH-0.001)/VH);
    for(let ty=dy0;ty<=dy1;ty++)for(let tx=dx0;tx<=dx1;tx++){
      const dc=this.ensureDeco(tx,ty);
      if(dc)g.drawImage(dc,tx*VW-cx,ty*VH-cy);
    }
  }
  const items=[];
  // 墙已含在地形图中，不再渲染障碍物精灵
  for(const m of this.monsters)if(m.state==='idle')items.push({y:m.y,k:'m',o:m});
  for(const c of this.chests)items.push({y:c.y,k:'c',o:c});
  for(const gte of this.gates)items.push({y:gte.y,k:'g',o:gte});
  for(const d of this.drops)items.push({y:d.y,k:'d',o:d});
  for(const id in this.remote){const r=this.remote[id];items.push({y:r.y,k:'r',o:r});}
  items.push({y:this.hero.y,k:'h',o:this.hero});
  items.sort((a,b)=>a.y-b.y);
  for(const d of this.dust){
    const sx=d.x-cx, sy=d.y-cy;
    const k=Math.max(0,d.life/d.max);
    const a=k*0.5;
    g.fillStyle=`rgba(232,216,180,${a})`;
    g.beginPath();g.arc(sx,sy,d.r*(2.0-k),0,Math.PI*2);g.fill();
  }
  for(const it of items){
    const sx=it.o.x-cx,sy=it.o.y-cy;
    if(sx<-180||sx>VW+180||sy<-180||sy>VH+180)continue;
    switch(it.k){
      case 'o':this.drawWall(g,it.o,sx,sy);break;
      case 'm':this.drawMonster(g,it.o,sx,sy,t);break;
      case 'c':this.drawChest(g,it.o,sx,sy,t);break;
      case 'g':this.drawGate(g,it.o,sx,sy,t);break;
      case 'd':this.drawChest(g,{x:it.o.x,y:it.o.y,tier:it.o.tier,state:'closed',phase:0,actor:it.o.actor},sx,sy,t,true);break;
      case 'r':this.drawRemote(g,it.o,sx,sy,t);break;
      case 'h':this.drawHero(g,sx,sy,t);break;
    }
  }
  this.progress=Math.min(20,this.visited.size);
};

WMap.prototype.drawWall=function(g,o,sx,sy){
  const r=o.r;
  g.fillStyle='rgba(0,0,0,.20)';g.beginPath();g.ellipse(sx+7,sy+12,r,r*0.5,0,0,Math.PI*2);g.fill();
  switch(this.wallKind){
    case 'tree':{
      g.fillStyle='#6b4a2a';g.fillRect(sx-6,sy,r*0.35,r*0.5);
      g.fillStyle=o.v?'#2e6b34':'#3a7d3c';g.beginPath();g.arc(sx,sy-8,r,0,Math.PI*2);g.fill();
      g.fillStyle=o.v?'#3f8a45':'#4b9a4d';g.beginPath();g.arc(sx-8,sy-16,r*0.7,0,Math.PI*2);g.fill();
      g.fillStyle=o.v?'#57a95a':'#66b869';g.beginPath();g.arc(sx-12,sy-20,r*0.4,0,Math.PI*2);g.fill();break;
    }
    case 'ice':{
      g.fillStyle='#bfe0f2';g.beginPath();g.moveTo(sx,sy-r*1.4);g.lineTo(sx+r*0.7,sy+6);g.lineTo(sx-r*0.7,sy+6);g.closePath();g.fill();
      g.fillStyle='#e8f6ff';g.beginPath();g.moveTo(sx,sy-r*1.4);g.lineTo(sx+r*0.2,sy+2);g.lineTo(sx-r*0.7,sy+6);g.closePath();g.fill();break;
    }
    case 'dune':{
      g.fillStyle='#a68d68';g.beginPath();g.ellipse(sx,sy,r*1.3,r*0.8,0,0,Math.PI*2);g.fill();
      g.fillStyle='#c2a87f';g.beginPath();g.ellipse(sx-6,sy-6,r,r*0.6,0,0,Math.PI*2);g.fill();break;
    }
    case 'meteor':{
      g.fillStyle='#3a3550';g.beginPath();g.arc(sx,sy-6,r,0,Math.PI*2);g.fill();
      g.fillStyle='#5b5478';g.beginPath();g.arc(sx-8,sy-14,r*0.55,0,Math.PI*2);g.fill();
      g.fillStyle=o.v?'#f0d590':'#9ad8ff';g.fillRect(sx-3,sy-3,6,6);break;
    }
    case 'cliff':{
      g.fillStyle='#8a5e3c';g.beginPath();g.moveTo(sx-r,sy+8);g.lineTo(sx-r*0.5,sy-r);g.lineTo(sx+r*0.6,sy-r*0.8);g.lineTo(sx+r,sy+6);g.closePath();g.fill();
      g.fillStyle='#a87650';g.beginPath();g.moveTo(sx-r*0.5,sy-r);g.lineTo(sx+r*0.6,sy-r*0.8);g.lineTo(sx+r*0.2,sy-r*0.2);g.closePath();g.fill();break;
    }
    default:{ // stone
      g.fillStyle=o.v?'#87909a':'#77818c';g.beginPath();g.moveTo(sx-r,sy+6);g.lineTo(sx-r*0.6,sy-r*0.8);g.lineTo(sx+r*0.5,sy-r);g.lineTo(sx+r,sy+4);g.closePath();g.fill();
      g.fillStyle='#a4adb6';g.beginPath();g.moveTo(sx-r*0.6,sy-r*0.8);g.lineTo(sx+r*0.5,sy-r);g.lineTo(sx+r*0.2,sy-r*0.2);g.closePath();g.fill();
    }
  }
};
WMap.prototype.drawHero=function(g,sx,sy,t){
  // 柔和投影：贴合脚底、居中收窄，避免深黑大椭圆压在角色下方
  const sh=g.createRadialGradient(sx,sy+10,1,sx,sy+10,15);
  sh.addColorStop(0,'rgba(30,38,28,.30)');
  sh.addColorStop(1,'rgba(30,38,28,0)');
  g.save();g.translate(sx,sy+10);g.scale(1,0.42);g.translate(-sx,-(sy+10));
  g.fillStyle=sh;g.beginPath();g.arc(sx,sy+10,15,0,Math.PI*2);g.fill();g.restore();
  if(this.heroActor)this.heroActor.render(g,sx,sy,2,{dir:this.hero.dir==='left'?-1:1});
};
WMap.prototype.drawMonster=function(g,m,sx,sy,t){
  const bob=Math.sin(t*3+m.phase)*3;
  const near=this.hero&&Math.hypot(m.x-this.hero.x,m.y-this.hero.y)<m.alert;
  // 阴影
  g.fillStyle='rgba(0,0,0,.26)';
  g.beginPath();g.ellipse(sx,sy+10,24,9,0,0,Math.PI*2);g.fill();
  if(m.actor)m.actor.render(g,sx,sy+bob,m.type==='boss_beetle'?2.6:2.1,{});
  // 警戒圈：仅当玩家靠近时显示，避免满屏红圈
  if(near){
    g.strokeStyle='rgba(255,110,90,.30)';g.lineWidth=2;g.setLineDash([5,7]);
    g.beginPath();g.arc(sx,sy,m.alert,0,Math.PI*2);g.stroke();g.setLineDash([]);
  }
  // 名牌：深色底 + 金色字（纯描边在浅色地面上看不清）
  g.font='bold 12px "Microsoft YaHei","PingFang SC",sans-serif';
  g.textAlign='center';
  const label=m.name;
  const w=g.measureText(label).width+14;
  const ny=sy-(m.type==='boss_beetle'?150:118);
  g.fillStyle='rgba(8,12,20,.72)';
  g.beginPath();
  const r=5;
  g.moveTo(sx-w/2+r,ny-9);g.lineTo(sx+w/2-r,ny-9);g.quadraticCurveTo(sx+w/2,ny-9,sx+w/2,ny-9+r);
  g.lineTo(sx+w/2,ny+7-r);g.quadraticCurveTo(sx+w/2,ny+7,sx+w/2-r,ny+7);
  g.lineTo(sx-w/2+r,ny+7);g.quadraticCurveTo(sx-w/2,ny+7,sx-w/2,ny+7-r);
  g.lineTo(sx-w/2,ny-9+r);g.quadraticCurveTo(sx-w/2,ny-9,sx-w/2+r,ny-9);
  g.closePath();g.fill();
  g.fillStyle=m.elite?'#f0d590':'#e8eef8';
  g.fillText(label,sx,ny+3);
  // 精英标记：小三角（替代 emoji ❗，避免字体缺字）
  if(m.elite){
    g.fillStyle='#ff6b5a';
    g.beginPath();g.moveTo(sx+w/2+6,ny-10);g.lineTo(sx+w/2+14,ny-10);g.lineTo(sx+w/2+10,ny-2);g.closePath();g.fill();
  }
};
WMap.prototype.drawChest=function(g,c,sx,sy,t,isDrop){
  const bob=isDrop?0:Math.sin(t*2+c.phase)*2;
  g.fillStyle='rgba(0,0,0,.28)';g.beginPath();g.ellipse(sx,sy+8,22,8,0,0,Math.PI*2);g.fill();
  if(c.actor)c.actor.render(g,sx,sy+bob,2.2,{});
  if(c.state==='closed'&&!isDrop){
    const glow=0.5+0.5*Math.sin(t*4+c.phase);
    g.strokeStyle=`rgba(240,213,144,${0.3+glow*0.4})`;g.lineWidth=2;
    g.beginPath();g.arc(sx,sy-10,36,0,Math.PI*2);g.stroke();
  }
  if(c.state==='opening'||c.state==='opening_done'){
    for(let i=0;i<6;i++){const a=t*3+i*1.05,rr=28+i*4;g.fillStyle=i%2?'#f0d590':'#7fe0a0';g.fillRect(sx+Math.cos(a)*rr-3,sy-28-Math.sin(a)*rr*0.5-3,6,6);}
  }
};
WMap.prototype.drawGate=function(g,gt,sx,sy,t){
  const glow=0.5+0.5*Math.sin(t*2.2);
  // 光柱
  const beam=g.createLinearGradient(0,sy-140,0,sy+10);
  beam.addColorStop(0,'rgba(140,200,255,0)');
  beam.addColorStop(0.55,`rgba(140,200,255,${0.10+glow*0.10})`);
  beam.addColorStop(1,'rgba(140,200,255,.02)');
  g.fillStyle=beam;
  g.beginPath();g.moveTo(sx-26,sy+8);g.lineTo(sx-14,sy-130);g.lineTo(sx+14,sy-130);g.lineTo(sx+26,sy+8);g.closePath();g.fill();
  // 石拱（左右立柱 + 顶弧）
  g.fillStyle='#6b6a72';
  g.fillRect(sx-40,sy-64,14,70);
  g.fillRect(sx+26,sy-64,14,70);
  g.beginPath();g.arc(sx,sy-64,40,Math.PI,0);g.lineWidth=13;g.strokeStyle='#6b6a72';g.stroke();
  g.fillStyle='rgba(255,255,255,.12)';
  g.fillRect(sx-40,sy-64,4,70);g.fillRect(sx+26,sy-64,4,70);
  // 门内传送漩涡
  g.save();
  g.beginPath();g.arc(sx,sy-64,34,Math.PI,0);g.arc(sx,sy-3,34,0,Math.PI);g.closePath();g.clip();
  const swirl=g.createRadialGradient(sx,sy-30,4,sx,sy-30,40);
  swirl.addColorStop(0,`rgba(190,225,255,${0.55+glow*0.3})`);
  swirl.addColorStop(0.6,'rgba(90,150,220,.35)');
  swirl.addColorStop(1,'rgba(40,80,140,.08)');
  g.fillStyle=swirl;g.fillRect(sx-40,sy-72,80,80);
  g.strokeStyle=`rgba(200,235,255,${0.25+glow*0.25})`;g.lineWidth=2;
  for(let i=0;i<3;i++){
    g.beginPath();
    for(let a=0;a<Math.PI*2;a+=0.25){
      const r=10+i*9+a*2.4+t*0.8;
      const px=sx+Math.cos(a)*Math.min(34,r), py=sy-34+Math.sin(a)*Math.min(34,r)*0.9;
      a===0?g.moveTo(px,py):g.lineTo(px,py);
    }
    g.stroke();
  }
  g.restore();
  // 顶牌
  g.font='bold 12px "Microsoft YaHei","PingFang SC",sans-serif';g.textAlign='center';
  const txt='⇄ '+gt.label;
  const w=g.measureText(txt).width+16;
  g.fillStyle='rgba(8,12,20,.78)';
  g.fillRect(sx-w/2,sy-118,w,20);
  g.strokeStyle='rgba(160,210,255,.6)';g.lineWidth=1;g.strokeRect(sx-w/2,sy-118,w,20);
  g.fillStyle='#cfe6ff';g.fillText(txt,sx,sy-104);
  if(gt.requireLevel>0){
    g.font='10px "Microsoft YaHei",sans-serif';
    g.fillStyle=gt.requireLevel>((this.hero&&this.lv)||99)?'#ff9a8f':'#ffd590';
    g.fillText('需 Lv.'+gt.requireLevel,sx,sy-84);
  }
};
WMap.prototype.drawRemote=function(g,r,sx,sy,t){
  g.fillStyle='rgba(0,0,0,.25)';g.beginPath();g.ellipse(sx,sy+10,18,7,0,0,Math.PI*2);g.fill();
  if(r.actor)r.actor.render(g,sx,sy,2,{});
  g.font='bold 12px sans-serif';g.textAlign='center';g.strokeStyle='#000';g.lineWidth=3;
  g.strokeText(r.name||'玩家',sx,sy-120);g.fillStyle='#8fd0ff';g.fillText(r.name||'玩家',sx,sy-120);
};
WMap.prototype.drawMinimapData=function(){
  const tx=Math.floor(this.hero.x/160),ty=Math.floor(this.hero.y/160);
  this.progress=Math.min(20,this.visited.size);
};

/* 多人：更新远端玩家快照（视野裁剪：仅 ±1 屏内玩家才渲染/维护） */
WMap.prototype.applySnapshot=function(players,selfId){
  const seen={};
  const hx=this.hero.x,hy=this.hero.y;
  // 交付包：视野渲染范围以角色为中心 ±1 屏，相机视野不随世界放大
  const rx=VW, ry=VH;
  for(const p of players){
    if(p.id===selfId)continue;
    if(Math.abs(p.x-hx)>rx||Math.abs(p.y-hy)>ry)continue; // 视野外不渲染
    seen[p.id]=true;
    if(!this.remote[p.id]){
      const actor=new this.PartActor('char_hero_mage');
      actor.init().then(()=>actor.play('idle'));
      this.remote[p.id]={x:p.x,y:p.y,name:p.name,actor};
    } else {
      const r=this.remote[p.id];
      // 插值：向目标坐标平滑移动（网络姿态平滑）
      r.x+=(p.x-r.x)*0.3;r.y+=(p.y-r.y)*0.3;
      r.name=p.name;
    }
  }
  for(const id in this.remote)if(!seen[id])delete this.remote[id];
};

window.WorldMap=WMap;
window.WorldMapConst={MW,MH,VW,VH,THEMES,MINI_W,MINI_H,MINI_SCALE,THUMB_W,THUMB_H,THUMB_SCALE};
})();

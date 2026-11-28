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
// v1.9.68 旗标到达判定：1 格 = 160 世界单位，取 150 免在"差一步"处抖
const PIN_ARRIVE_R=150;
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
const MON_NAMES={slime:'史莱姆',skeleton:'骷髅兵',bat:'蝙蝠',boss_beetle:'甲虫将军',
  slime_toxic:'毒液史莱姆',skeleton_archer:'骷髅弓手',bat_crimson:'血蝙蝠',map_boss:'图域首领'};
// v1.9.46 贴图别名：怪物 id 与像素资源包名不一致时在此登记（否则会 404 请求 mon_map_boss/parts.json）
const MON_SPRITE={map_boss:'boss_beetle'};

function WMap(canvas, opts){
  this.cv=canvas;
  this.ctx=canvas.getContext('2d');
  this.ctx.imageSmoothingEnabled=false;
  this.opts=opts||{};
  this.onEncounter=this.opts.onEncounter||function(){};
  this.onChest=this.opts.onChest||function(){};
  this.onGate=this.opts.onGate||function(){};
  this.onTile=this.opts.onTile||function(){};

  this.hero={x:SPAWN_X,y:SPAWN_Y,dir:'right',moving:false};
  this.keys={};
  this.cam={x:0,y:0};
  this.worldId='W1';
  this.theme=THEMES.grassland;
  this.themeKey='grassland';
  // v1.9.31 家园：世界尺寸改为实例属性（HOME=2560×1440 小地图，其余世界=MW/MH）
  this.WM=MW; this.HM=MH;
  this.homeItems=[];  // 家园功能点 {x,y,nodeId,kind,label}（仓库/挂机/换装镜）

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
  // v1.9.89 边缘方位箭头专用层（#pinEdgeCv）：贴在画布之上、但高于底部导航/小地图。
  // 之前箭头和地图画在同一个 canvas 上，层级天然低于 HUD，正下方目标的箭头会被底部
  // 导航整块盖住 → 用户完全看不到提示。单独一层后位置仍严格贴边（20px），且始终可见。
  this.edgeCv=(typeof document!=='undefined')?document.getElementById('pinEdgeCv'):null;
  this.edgeG=this.edgeCv?this.edgeCv.getContext('2d'):null;
  // v1.9.90 底部黑条高度（主界面可见时才有；其它视图返回 0，箭头落点自动回落到底边）
  this.dockHeight=()=>{
    const d=(typeof document!=='undefined')?document.getElementById('bottomDock'):null;
    return (d&&d.offsetParent)?d.offsetHeight:0;
  };
  this.PartActor=window.PartActor;
  this.StandeeActor=window.StandeeActor; // 主角/职业装走整体立绘（交付包 §0.2）
  this.heroBundle='char_warrior';        // 由 classId 决定（char_warrior/char_mage/…）
  this.actorCache={}; // bundle -> PartActor 实例（怪物/宝箱每类一个）

  // v1.9.64 地图旗标：大地图上点选地点后可选择是否跟踪（仅存本地，不落服务端）
  // v1.9.82 改为多标记点：[{id,wid,x,y,track}]；wid 用于切图后不误显示别的地图的标记
  this.pins=[];
  try{
    const s=localStorage.getItem('zjcs.mapPins');
    if(s){ const a=JSON.parse(s); if(Array.isArray(a))this.pins=a.filter(p=>p&&p.wid&&isFinite(p.x)&&isFinite(p.y)); }
    else{
      // 旧版单个旗标（zjcs.mapPin）迁移成数组，迁完即删旧键，避免下次重复导入
      const o=localStorage.getItem('zjcs.mapPin');
      if(o){ const p=JSON.parse(o);
        if(p&&p.wid&&isFinite(p.x)&&isFinite(p.y))this.pins=[{id:'p_legacy',wid:p.wid,x:p.x,y:p.y,track:!!p.track}];
        localStorage.removeItem('zjcs.mapPin');
      }
    }
  }catch(e){}
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
    const maskImg=await loadImg(base+'mask_half.png?v=1.9.5');
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
  const url=RES+'maps/'+worldId+'_full_half.webp?v=1.9.5';
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
  // v1.9.31 动态缩略尺度（app.js 小地图 / M 弹窗按此换算；常规世界=THUMB_*）
  this.thumbW=THUMB_W; this.thumbH=THUMB_H; this.thumbScale=THUMB_SCALE;
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
  } else { if(this._pending)this._pending.delete(key); }
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
    if(self._decoPending)self._decoPending.delete(key); // v1.9.23 守卫：切世界瞬间 _decoPending 被置空，旧回调不再炸 null
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
  // 预载角色立绘 actor（整体立绘 + root 变换；换职业时由 setHeroBundle 重建）
  if(!this.heroActor){
    this.heroActor=new this.StandeeActor(this.heroBundle);
    await this.heroActor.init();
    this.heroActor.play('idle');
  }
  await this.loadTerrain(this.worldId);
  this.centerCam();
  this.ready=true;
  if(!this._inputBound){this.bindInput();this._inputBound=true;}
};

/* 换职业：重建主角立绘（探索/战斗同一套职业装备装，交付包 §4.2） */
WMap.prototype.setHeroBundle=function(bundle){
  if(!bundle||bundle===this.heroBundle)return;
  this.heroBundle=bundle;
  const a=new this.StandeeActor(bundle);
  a.init().then(()=>a.play('idle'));
  this.heroActor=a;
};
/* v1.9.27 设置美术默认朝向；v1.9.57 默认"朝右"（当前六职业立绘全部朝右） */
WMap.prototype.setHeroFacing=function(facesRight){
  this.heroFacesRight=facesRight!==false;
};

/* ---------- 切换世界 + 接入服务端点位 ---------- */
WMap.prototype.switchWorld=async function(worldId, spots, heroX, heroY){
  this.worldId=worldId;
  const wdef=(this.opts.worldDefs||[]).find(w=>w.id===worldId);
  const themeKey=wdef?wdef.theme:'grassland';
  this.themeKey=themeKey;
  this.theme=THEMES[themeKey]||THEMES.grassland;
  // v1.9.31 家园：小地图 + 程序化场景（无地形资源，全图可走）
  if(worldId==='HOME'){
    this.WM=2560; this.HM=1440;
    await this.loadHomeTerrain();
  } else {
    this.WM=MW; this.HM=MH;
    await this.loadTerrain(worldId);
  }
  // 重置实体
  this.monsters=[];this.chests=[];this.potions=[];this.gates=[];this.anchors=[];this.drops=[];this.homeItems=[];
  if(heroX!=null&&heroY!=null){this.hero.x=heroX;this.hero.y=heroY;}
  else {this.hero.x=SPAWN_X;this.hero.y=SPAWN_Y;} // 默认落安全区
  // 若存档坐标落在墙里（旧存档 / 锚点压在掩码边界上），就近找一块可站立的地面，
  // 而不是粗暴回落到 W1 的 SPAWN 坐标（那会在 W6 之类的世界里直接落进湖里/墙里）
  if(this.blocked(this.hero.x,this.hero.y,24)){
    const fixed=this.findWalkableNear(this.hero.x,this.hero.y,720);
    this.hero.x=fixed.x;this.hero.y=fixed.y;
  }
  this.applySpots(spots||[]);
  this.centerCam();
  this.prewarmTiles(); // 后台预热分块，避免移动跨块时同步建块掉帧
};

/* v1.9.31 家园场景：程序化绘制 2560×1440 小地图（草地/木屋/栅栏/花坛/小路），
   无地形资源、无通行掩码（maskData=null → 全图可走，边界由 update 钳制）。
   缩略图与常规世界同规格 960×540，供小地图与 M 大地图弹窗复用。 */
WMap.prototype.loadHomeTerrain=async function(){
  // 清理常规世界资源
  this._pwToken++;
  if(this.thumb&&this.thumb.close){try{this.thumb.close();}catch(e){}}
  this.maskCv=null; this.paintCv=null; this.waterCv=null;
  if(this.deco)this.deco.clear();
  this.deco=null; this._decoPending=null; this.pal=null;
  this.tiles=null;this._pending=null;
  if(this.groundBmp&&this.groundBmp.close){try{this.groundBmp.close();}catch(e){}}
  this.groundBmp=null;this.ground=null;
  this.mapCache['HOME']=null;

  const HW=this.WM, HH=this.HM;
  const off=document.createElement('canvas');
  off.width=HW; off.height=HH;
  const g=off.getContext('2d');
  let seed=20261126;
  const rnd=()=>{seed=(seed*9301+49297)%233280;return seed/233280;};
  // 草地底色（与 grassland 主题同色系，颗粒噪点）
  g.fillStyle='#79ad4a';g.fillRect(0,0,HW,HH);
  for(let y=0;y<HH;y+=64)for(let x=0;x<HW;x+=64){
    g.fillStyle=['#7fb350','#74a746','#85b755','#79ad4a'][Math.floor(rnd()*4)];
    g.fillRect(x,y,64,64);
  }
  // 十字小路（浅石板色）
  g.strokeStyle='#c9bb96';g.lineWidth=110;g.lineCap='round';
  g.beginPath();g.moveTo(0,780);g.lineTo(HW,780);g.stroke();
  g.beginPath();g.moveTo(1280,0);g.lineTo(1280,HH);g.stroke();
  g.fillStyle='rgba(60,50,30,.15)';
  for(let i=0;i<160;i++){const x=rnd()*HW,y=780+(rnd()-0.5)*90;g.fillRect(x,y,10,4);}
  // 花丛点缀
  const fc=['#f5f0d8','#f0d590','#e8a0b8'];
  for(let i=0;i<220;i++){
    const x=rnd()*HW,y=rnd()*HH;
    if(Math.abs(y-780)<80)continue;
    g.fillStyle=fc[Math.floor(rnd()*fc.length)];
    g.fillRect(x,y,4,4);g.fillRect(x-2,y+3,2,2);g.fillRect(x+4,y+3,2,2);
  }
  // 房屋（左上角：木屋 + 门 + 窗）
  const hx=300,hy=180;
  g.fillStyle='rgba(0,0,0,.18)';g.beginPath();g.ellipse(hx+90,hy+240,130,30,0,0,Math.PI*2);g.fill();
  g.fillStyle='#8a5a34';g.fillRect(hx,hy+120,180,120);          // 墙
  g.fillStyle='#6d4426';
  g.beginPath();g.moveTo(hx-24,hy+120);g.lineTo(hx+90,hy+20);g.lineTo(hx+204,hy+120);g.closePath();g.fill(); // 屋顶
  g.fillStyle='#5a3a12';g.fillRect(hx+70,hy+170,44,70);          // 门
  g.fillStyle='#f0d590';g.fillRect(hx+130,hy+150,30,26);         // 窗
  // 栅栏（围出院子轮廓）
  g.fillStyle='#a07948';
  const fence=(x0,y0,x1,y1)=>{const n=Math.floor(Math.hypot(x1-x0,y1-y0)/44);for(let i=0;i<=n;i++){const x=x0+(x1-x0)*i/n,y=y0+(y1-y0)*i/n;g.fillRect(x-4,y-26,8,30);g.fillRect(x-4,y-20,8,26);}};
  fence(80,320,80,1380);fence(80,320,460,320);fence(2480,320,2480,1380);fence(2100,320,2480,320);
  fence(80,1420,2480,1420);fence(80,1420,120,1440);fence(2440,1420,2480,1440);
  // 小池塘（右下）
  g.fillStyle='#3f7fa8';g.beginPath();g.ellipse(1650,1180,120,80,0,0,Math.PI*2);g.fill();
  g.fillStyle='rgba(255,255,255,.18)';g.beginPath();g.ellipse(1630,1165,84,52,0,0,Math.PI*2);g.fill();
  this.groundHome=off;
  // 通行掩码清空 → 全图可走（blocked 对 null 掩码恒 false）
  this.maskData=null;
  this.waterFlag=null;
  // 缩略图（960×540，与常规世界同规格：M 大地图弹窗/小地图共用）
  this.thumbW=THUMB_W; this.thumbH=THUMB_H; this.thumbScale=HW/THUMB_W;
  this._mapThumbFallback=null;
  try{
    this.thumb=await createImageBitmap(off,{resizeWidth:THUMB_W,resizeHeight:THUMB_H,resizeQuality:'low'});
  }catch(e){ this.thumb=null; this._mapThumbFallback=off; }
  this.centerCam();
};

/* 分块预热：从角色所在块螺旋向外全部异步请求（createImageBitmap 走解码线程，
   主线程零开销，不再与渲染争抢）；未预热完的帧由主题底色兜底 */
WMap.prototype.prewarmTiles=function(){
  if(!this.tiles)return;
  const cols=this.WM/VW, rows=this.HM/VH;
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
  // v1.9.23 点位落点自检：配置坐标可能压在地图边缘（碰撞体采样越界=墙，如 x=7664 距右边缘仅
  // 16px、W2→W4 通道 y=0 压顶边）或落在水域/墙体里（掩码水域在运行时被就地掩黑为不可走），
  // 导致 GATE 看不见或走不进去。就近螺旋找一块碰撞可站立的落点自动修正（含避水）。
  const fixPos=(raw)=>{
    if(!this.maskData||!this.blocked(raw.x,raw.y,24))return raw;
    const f=this.findWalkableNear(raw.x,raw.y,960);
    return f?Object.assign({},raw,{x:f.x,y:f.y}):raw;
  };
  for(const raw of spots){
    const s=raw.type==='ANCHOR'?raw:fixPos(raw);
    if(s.type==='ANCHOR'){this.anchors.push(s);continue;}
    if(s.type==='GATE'){
      const wdefs=(this.opts&&this.opts.worldDefs)||[];
      const tw=wdefs.find(w=>w.id===s.to);
      this.gates.push({x:s.x,y:s.y,nodeId:s.nodeId,to:s.to,requireLevel:s.reqLevel||s.requireLevel||0,
        label:'通往 '+(tw?tw.name:(s.to||'?')),worldName:tw?tw.name:s.to});
      continue;
    }
    if(s.type==='MOB'||s.type==='ELITE'||s.type==='BOSS'){
      const monId=s.contentId||s.bossMonster||'slime';
      // v1.9.36 群怪模版：点位组合随服务端下发（group），遭遇贴图与战斗结算同源
      const group=(s.group&&s.group.length)?s.group:[monId];
      // v1.9.39 修复：贴图取组合内第一只怪（与实际战斗一致），不再用点位原类型。
      // 此前用 monId 选贴图、group 算名字 → 出现"名字骷髅兵、贴图史莱姆"的错位。
      const firstMon=group[0]||monId;
      // v1.9.46：先查贴图别名（map_boss → boss_beetle），再回退按 id 取名
      const alias=MON_SPRITE[firstMon]||MON_SPRITE[monId];
      const sprite=alias||(MON_NAMES[firstMon]?firstMon:(MON_NAMES[monId]?monId:'slime'));
      const actor=this.makeFreshActor('mon_'+sprite,'idle');
      // 组合名：按怪物 id 计数（如"史莱姆×2 · 骷髅×1"）
      const cnt={};group.forEach(g=>{cnt[g]=(cnt[g]||0)+1;});
      const gangName=Object.keys(cnt).map(k=>(MON_NAMES[k]||k)+'×'+cnt[k]).join(' · ');
      // v1.9.46 三档怪物：normal/elite/boss，掉落档位、等级加成、标签都以此为准
      const kind=s.type==='BOSS'?'boss':(s.type==='ELITE'?'elite':'normal');
      // v1.9.46 首领随机落点：每次进入地图在可行走区域随机刷新，避免固定蹲点被卡位刷
      let px=s.x, py=s.y;
      if(kind==='boss'&&s.randomPos){ const rp=this.randomBossPoint(); if(rp){px=rp.x;py=rp.y;} }
      const pre=kind==='elite'?'精英 ':(kind==='boss'?'首领 ':'');
      this.monsters.push({x:px,y:py,nodeId:s.nodeId,type:sprite,name:pre+gangName,
        group,gangName,monId,kind,elite:kind!=='normal',
        state:'idle',r:kind==='boss'?44:30,
        alert:kind==='boss'?440:(kind==='elite'?360:300),actor,phase:Math.random()*6});
      continue;
    }
    if(s.type==='CHEST'){
      // v1.9.46 修复：此前 Math.min(2,…) 把档位截断在 gold，钻石/水晶箱点位永远显示黄金箱
      const tier=['wood','silver','gold','diamond','crystal'][Math.max(0,Math.min(4,(s.tier||1)-1))]||'wood';
      const actor=this.makeFreshActor('item_chest_'+tier,'closed');
      this.chests.push({x:s.x,y:s.y,nodeId:s.nodeId,tier,state:'closed',actor,phase:Math.random()*6});
    }
    // v1.9.27 草药点位：采集得回血药水，服务端 2 分钟 respawn
    if(s.type==='POTION'){
      this.potions.push({x:s.x,y:s.y,nodeId:s.nodeId,state:'ready',phase:Math.random()*6});
    }
    // v1.9.31 家园功能点：content=warehouse/idle/dress → homeItems；gate → 复用传送门（出门）
    if(s.type==='FUNC'){
      const kind=s.contentId||'warehouse';
      if(kind==='gate'){
        this.gates.push({x:s.x,y:s.y,nodeId:s.nodeId,to:'BACK',requireLevel:0,
          label:'出门 · 返回大世界',worldName:'大世界'});
      } else {
        const labels={warehouse:'仓库',idle:'挂机修炼',dress:'换装镜'};
        this.homeItems.push({x:s.x,y:s.y,nodeId:s.nodeId,kind,label:labels[kind]||kind,phase:Math.random()*6});
      }
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
  this.cam.x=Math.max(0,Math.min(this.WM-VW,this.hero.x-VW/2));
  this.cam.y=Math.max(0,Math.min(this.HM-VH,this.hero.y-VH/2));
};
/* 就近可站立点：以 (x,y) 为心螺旋外扩，返回第一个碰撞半径 24 全通的位置 */
WMap.prototype.findWalkableNear=function(x,y,maxR){
  const lim=maxR||720;
  for(let r=16;r<=lim;r+=16){
    for(let a=0;a<360;a+=18){
      const rad=a*Math.PI/180;
      const nx=x+Math.cos(rad)*r, ny=y+Math.sin(rad)*r;
      if(nx<40||ny<40||nx>this.WM-40||ny>this.HM-40)continue;
      if(!this.blocked(nx,ny,24))return {x:nx,y:ny};
    }
  }
  return {x:Math.max(40,Math.min(this.WM-40,x)),y:Math.max(40,Math.min(this.HM-40,y))};
};
/* v1.9.46 首领随机落点：在可行走区域随机采样，避开出生锚点周围 900 单位，
   采样 80 次仍失败（水域/密林极多的图）则返回 null，回落到配置坐标。 */
WMap.prototype.randomBossPoint=function(){
  const margin=220, minFromAnchor=900;
  const anchor=(this.anchors&&this.anchors[0])||null;
  for(let i=0;i<80;i++){
    const x=margin+Math.random()*(this.WM-margin*2);
    const y=margin+Math.random()*(this.HM-margin*2);
    if(this.blocked(x,y,44))continue;
    if(anchor&&Math.hypot(x-anchor.x,y-anchor.y)<minFromAnchor)continue;
    return {x:Math.round(x),y:Math.round(y)};
  }
  return null;
};

/* ---------- 输入 ---------- */
WMap.prototype.bindInput=function(){
  const self=this;
  addEventListener('keydown',e=>{
    if(!self.active)return;
    if(self.mapModalOpen)return; // v1.9.29 大地图弹窗打开时锁定移动
    const k=e.key.toLowerCase();
    if(['w','a','s','d','arrowup','arrowdown','arrowleft','arrowright'].includes(k)){self.keys[k]=true;e.preventDefault();}
  });
  addEventListener('keyup',e=>{self.keys[e.key.toLowerCase()]=false;});
  this.cv.addEventListener('click',e=>{
    if(!self.active)return;
    const rect=this.cv.getBoundingClientRect();
    const sx=(e.clientX-rect.left)/rect.width*VW, sy=(e.clientY-rect.top)/rect.height*VH;
    const wx=sx+self.cam.x, wy=sy+self.cam.y;
    // v1.9.27 点击分发改为"取最近的可交互物"：草药与宝箱/怪点位可能相邻，
    // 旧的固定顺序（门→怪→箱→草药）会让近处草药被远处宝箱"抢走"点击。
    const cands=[];
    const gate=self.nearestGate(wx,wy,200);
    if(gate)cands.push({d:Math.hypot(gate.x-wx,gate.y-wy),fn:()=>self.opts.onGate&&self.opts.onGate(gate)});
    const mon=self.nearestMonster(wx,wy,190);
    if(mon)cands.push({d:Math.hypot(mon.x-wx,mon.y-wy),fn:()=>self.opts.onEncounter&&self.opts.onEncounter(mon)});
    const ch=self.nearestChest(wx,wy,190);
    if(ch)cands.push({d:Math.hypot(ch.x-wx,ch.y-wy),fn:()=>self.tryOpenChest(ch)});
    const pt=self.nearestPotion(wx,wy,190);
    if(pt)cands.push({d:Math.hypot(pt.x-wx,pt.y-wy),fn:()=>self.tryPickPotion(pt)});
    // v1.9.31 家园功能点（仓库/挂机/换装镜）
    const hi=self.nearestHomeItem(wx,wy,200);
    if(hi)cands.push({d:Math.hypot(hi.x-wx,hi.y-wy),fn:()=>self.tryUseHomeItem(hi)});
    // v1.9.42 点其他玩家 → 查看档案
    const rp=self.nearestRemote(wx,wy,160);
    if(rp)cands.push({d:Math.hypot(rp.x-wx,rp.y-wy),fn:()=>self.opts.onPlayer&&self.opts.onPlayer(rp)});
    if(cands.length){cands.sort((a,b)=>a.d-b.d);cands[0].fn();return;}
    // 已移除点击地面移动：仅保留 WASD/方向键移动，点击只用于交互（怪/箱/传送门）
  });
};
WMap.prototype.nearestGate=function(x,y,maxd){let b=null,d0=maxd;for(const g of this.gates){const d=Math.hypot(g.x-x,g.y-y);if(d<d0){d0=d;b=g;}}return b;};
WMap.prototype.nearestMonster=function(x,y,maxd){let b=null,d0=maxd||110;for(const m of this.monsters){if(m.state!=='idle')continue;const d=Math.hypot(m.x-x,m.y-y);if(d<d0){d0=d;b=m;}}return b;};
WMap.prototype.nearestChest=function(x,y,maxd){let b=null,d0=maxd||80;for(const c of this.chests){if(c.state!=='closed')continue;const d=Math.hypot(c.x-x,c.y-y);if(d<d0){d0=d;b=c;}}return b;};
WMap.prototype.nearestPotion=function(x,y,maxd){let b=null,d0=maxd||80;for(const p of this.potions){if(p.state!=='ready')continue;const d=Math.hypot(p.x-x,p.y-y);if(d<d0){d0=d;b=p;}}return b;};
WMap.prototype.nearestHomeItem=function(x,y,maxd){let b=null,d0=maxd||200;for(const h of this.homeItems){const d=Math.hypot(h.x-x,h.y-y);if(d<d0){d0=d;b=h;}}return b;};
WMap.prototype.nearestRemote=function(x,y,maxd){let b=null,d0=maxd||120;for(const id in this.remote){const r=this.remote[id];const d=Math.hypot(r.x-x,r.y-y);if(d<d0){d0=d;b=r;}}return b;};
/* v1.9.31 家园功能点交互：需走到附近（与宝箱/草药同规则） */
WMap.prototype.tryUseHomeItem=function(h){
  if(Math.hypot(h.x-this.hero.x,h.y-this.hero.y)>300){
    if(this.opts.onHint)this.opts.onHint('请用 WASD / 方向键走到'+(h.label||'设施')+'附近再使用');
    return;
  }
  this.opts.onHomeItem&&this.opts.onHomeItem(h);
};

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
    // 朝向：连续角度（小地图指针按此旋转，8 方向都不会被压成 4 档）
    if(dx||dy)h.ang=Math.atan2(dy,dx);
    // v1.9.52：朝向只由"是否含水平分量"决定，纯上下移动保持原朝向。
    // 原判据 |dx|>|dy| 在 45° 斜走（W+A / W+D / S+A / S+D）时恒为假，
    // 于是斜向右上走仍沿用上一次的 'left'，表现为"往右走却朝左"，反向同理。
    if(dx!==0)h.dir=dx>0?'right':'left';
    const nx=h.x+dx*spd,ny=h.y+dy*spd;
    // 掩码精度8px，角色碰撞半径取24（≈显示半宽，保证不穿墙）
    if(!this.blocked(nx,h.y,24))h.x=nx;
    if(!this.blocked(h.x,ny,24))h.y=ny;
    h.x=Math.max(30,Math.min(this.WM-30,h.x));h.y=Math.max(30,Math.min(this.HM-30,h.y));
    const tx=Math.floor(h.x/640),ty=Math.floor(h.y/640);
    this.onTile&&this.onTile(tx+','+ty);this.visited.add(tx+','+ty);
  } else if(this.heroActor&&this.heroActor.state==='walk'){this.heroActor.play('idle');}
  // 走路扬尘：每 90ms 在脚后生成一粒，短寿命淡出
  if(h.moving&&this.time-(this._dustTs||0)>0.09){
    this._dustTs=this.time;
    this.dust.push({x:h.x+(Math.random()-0.5)*18,y:h.y+4,vx:(Math.random()-0.5)*14,vy:-14-Math.random()*10,life:0.55,max:0.55,r:2+Math.random()*2.5});
    if(this.dust.length>26)this.dust.shift();
  }
  // 脚步声：与扬尘同源但更慢（约 3 步/秒），音量极低，只提供"脚踩地面"的存在感
  if(h.moving&&this.time-(this._stepTs||0)>0.33){
    this._stepTs=this.time;
    if(window.SFX&&SFX.step)SFX.step();
  }
  for(const d of this.dust){ d.x+=d.vx*dt; d.y+=d.vy*dt; d.vy+=26*dt; d.life-=dt; }
  this.dust=this.dust.filter(d=>d.life>0);
  this.centerCam();
  // v1.9.68 走到标记点附近 → 自动取消跟踪（标记本身保留，玩家可自行删除或重新跟踪）
  // v1.9.82 多个标记点：逐个判定，可能一次到达多个
  let arrived=0;
  for(const gp of this.worldPins()){
    if(gp.track&&Math.hypot(gp.x-h.x,gp.y-h.y)<=PIN_ARRIVE_R){
      gp.track=false; arrived++;
    }
  }
  if(arrived){
    this.savePins();
    this.pinToast={text:arrived>1?('已到达 '+arrived+' 个标记点 · 自动取消跟踪'):'已到达标记点 · 自动取消跟踪',until:this.time+3.0};
    if(window.SFX&&SFX.tab)SFX.tab();
  }
  // 掉落物理
  for(const d of this.drops){
    d.vy+=1400*dt;d.y+=d.vy*dt;
    if(d.y>=d.groundY){d.y=d.groundY;if(Math.abs(d.vy)>60)d.vy*=-0.42;else{this.chests.push({x:d.x,y:d.y,nodeId:d.nodeId||('drop_'+Date.now()),tier:d.tier,state:'closed',actor:d.actor||this.makeFreshActor('item_chest_'+d.tier,'closed'),phase:0,dropped:true});d.done=true;}}
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

/* v1.9.27 采集草药：走到附近点击 → 服务端权威入仓（回血药水） */
WMap.prototype.tryPickPotion=function(p){
  if(p.state!=='ready')return;
  if(Math.hypot(p.x-this.hero.x,p.y-this.hero.y)>260){
    if(this.opts.onHint)this.opts.onHint('请用 WASD / 方向键走到草药附近再采集');
    return;
  }
  p.state='picked';
  this.opts.onPotion&&this.opts.onPotion(p);
};
WMap.prototype.openChest=function(c){
  if(c.state!=='closed')return;
  c.state='opening';
  if(c.actor)c.actor.playOnce('open',()=>{c.state='opened';});
  this.opts.onChest&&this.opts.onChest(c);
};
// v1.9.47：箱档位与 nodeId 由 app.js 向服务端登记后传入，本层只做落地表现。
// 这里不再直接调 api()——api/uid 定义在 app.js 的 IIFE 内，本文件（全局脚本）取不到，
// 之前在此判断 typeof api==='function' 恒为 false，导致打怪后不落箱。
WMap.prototype.spawnChestDrop=function(mon,tier,nodeId){
  if(!tier) return false;
  this.drops.push({x:mon.x,y:mon.y-90,groundY:mon.y,tier,nodeId:nodeId||('drop_'+Date.now()),
    actor:this.makeFreshActor('item_chest_'+tier,'closed'),vy:-120});
  return true;
};
WMap.prototype.heroDefeated=function(){};

/* ---------- 渲染 ---------- */
WMap.prototype.render=function(now){
  if(!this.ready)return;
  const dt=Math.min(0.05,(now-this.lastTs)/1000||0);this.lastTs=now;this.time+=dt;
  this.update(dt);
  const g=this.ctx,cx=this.cam.x,cy=this.cam.y,t=this.time;
  // v1.9.31 家园：程序化整图直绘（无分块/掩码/装饰层）
  if(this.worldId==='HOME'&&this.groundHome){
    g.fillStyle='#3a5a2a';g.fillRect(0,0,VW,VH);
    g.drawImage(this.groundHome,cx,cy,VW,VH,0,0,VW,VH);
  }
  // 真实地形：分块纹理绘制（每帧仅 2×2 块视口大小的小纹理）。
  // 块未就绪时先铺主题底色（异步建块毫秒级返回，肉眼几乎不可见）
  else if(this.tiles){
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
  for(const p of this.potions)if(p.state==='ready')items.push({y:p.y,k:'p',o:p});
  for(const gte of this.gates)items.push({y:gte.y,k:'g',o:gte});
  for(const h of this.homeItems)items.push({y:h.y,k:'f',o:h}); // v1.9.31 家园功能点
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
      case 'p':this.drawPotion(g,it.o,sx,sy,t);break;
      case 'g':this.drawGate(g,it.o,sx,sy,t);break;
      case 'f':this.drawHomeItem(g,it.o,sx,sy,t);break;
      case 'd':this.drawChest(g,{x:it.o.x,y:it.o.y,tier:it.o.tier,state:'closed',phase:0,actor:it.o.actor},sx,sy,t,true);break;
      case 'r':this.drawRemote(g,it.o,sx,sy,t);break;
      case 'h':this.drawHero(g,sx,sy,t);break;
    }
  }
  this.drawPinHud(g,cx,cy,t);
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
  // 立绘逻辑画布 64×96：scale=1 即屏幕 64×96，与原主角显示尺寸/比例锚点一致
  // v1.9.27：行走方向按美术默认朝向修正——美术朝右者向左翻转，美术朝左者向右翻转
  if(this.heroActor){
    const f=this.heroFacesRight!==false;   // v1.9.57 默认"立绘朝右"
    const flip=this.hero.dir==='left'?(f?-1:1):(f?1:-1);
    this.heroActor.render(g,sx,sy,1,{dir:flip});
  }
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
/* v1.9.27 草药（回血药水原料）：闪光瓶子，采集后消失（服务端 2 分钟 respawn） */
WMap.prototype.drawPotion=function(g,p,sx,sy,t){
  const bob=Math.sin(t*2.4+p.phase)*3;
  const glow=0.5+0.5*Math.sin(t*3.5+p.phase);
  g.fillStyle='rgba(0,0,0,.26)';g.beginPath();g.ellipse(sx,sy+8,16,6,0,0,Math.PI*2);g.fill();
  // 光晕
  g.strokeStyle=`rgba(127,224,160,${0.25+glow*0.4})`;g.lineWidth=2;
  g.beginPath();g.arc(sx,sy-12+bob,30,0,Math.PI*2);g.stroke();
  // 瓶身（圆瓶 + 软木塞 + 药液）
  g.fillStyle='#c9b28a';g.fillRect(sx-5,sy-34+bob,10,7);                    // 木塞
  g.fillStyle='#bfe9d2';g.beginPath();g.arc(sx,sy-10+bob,14,0,Math.PI*2);g.fill(); // 瓶身
  g.fillStyle='#e5564b';g.beginPath();g.arc(sx,sy-8+bob,10,0,Math.PI*2);g.fill();  // 红药液
  g.fillStyle='rgba(255,255,255,.75)';g.beginPath();g.arc(sx-5,sy-15+bob,3,0,Math.PI*2);g.fill(); // 高光
};
/* v1.9.31 家园功能点：仓库木箱 / 挂机篝火 / 换装立镜（程序化绘制 + 悬浮名牌） */
WMap.prototype.drawHomeItem=function(g,h,sx,sy,t){
  const bob=Math.sin(t*2+h.phase)*2;
  const glow=0.5+0.5*Math.sin(t*3+h.phase);
  g.fillStyle='rgba(0,0,0,.24)';g.beginPath();g.ellipse(sx,sy+10,34,12,0,0,Math.PI*2);g.fill();
  if(h.kind==='warehouse'){
    // 木箱堆（大箱+小箱）
    g.fillStyle='#a8794a';g.fillRect(sx-34,sy-44+bob,68,52);
    g.strokeStyle='#6d4a24';g.lineWidth=3;g.strokeRect(sx-34,sy-44+bob,68,52);
    g.beginPath();g.moveTo(sx-34,sy-44+bob);g.lineTo(sx+34,sy+8+bob);g.moveTo(sx+34,sy-44+bob);g.lineTo(sx-34,sy+8+bob);g.stroke();
    g.fillStyle='#8a5f34';g.fillRect(sx+22,sy-16+bob,34,24);
    g.strokeStyle='#5a3a12';g.strokeRect(sx+22,sy-16+bob,34,24);
  } else if(h.kind==='idle'){
    // 篝火（柴堆 + 跳动火焰）
    g.strokeStyle='#7a5630';g.lineWidth=7;g.lineCap='round';
    g.beginPath();g.moveTo(sx-24,sy);g.lineTo(sx+20,sy-12);g.moveTo(sx+24,sy);g.lineTo(sx-20,sy-12);g.stroke();
    const fs=1+0.18*Math.sin(t*7+h.phase);
    g.fillStyle=`rgba(240,140,60,${0.75+glow*0.25})`;
    g.beginPath();g.moveTo(sx,sy-58*fs+bob);g.quadraticCurveTo(sx+16,sy-26+bob,sx,sy-4+bob);g.quadraticCurveTo(sx-16,sy-26+bob,sx,sy-58*fs+bob);g.fill();
    g.fillStyle=`rgba(255,214,90,${0.8+glow*0.2})`;
    g.beginPath();g.moveTo(sx,sy-36*fs+bob);g.quadraticCurveTo(sx+9,sy-18+bob,sx,sy-4+bob);g.quadraticCurveTo(sx-9,sy-18+bob,sx,sy-36*fs+bob);g.fill();
    g.strokeStyle=`rgba(255,180,90,${0.25+glow*0.4})`;g.lineWidth=2;
    g.beginPath();g.arc(sx,sy-20,42,0,Math.PI*2);g.stroke();
  } else if(h.kind==='dress'){
    // 换装立镜（木框 + 镜面 + 高光）
    g.fillStyle='#8a5f34';
    g.beginPath();g.roundRect?g.roundRect(sx-26,sy-92+bob,52,100,12):g.rect(sx-26,sy-92+bob,52,100);g.fill();
    g.fillStyle='#bcd8e8';g.fillRect(sx-18,sy-84+bob,36,84);
    g.fillStyle='rgba(255,255,255,.55)';
    g.beginPath();g.moveTo(sx-14,sy-80+bob);g.lineTo(sx-2,sy-80+bob);g.lineTo(sx-14,sy-40+bob);g.closePath();g.fill();
    g.strokeStyle=`rgba(160,220,255,${0.2+glow*0.4})`;g.lineWidth=2;
    g.beginPath();g.arc(sx,sy-44+bob,46,0,Math.PI*2);g.stroke();
  }
  // 悬浮名牌
  g.font='bold 13px "Microsoft YaHei","PingFang SC",sans-serif';g.textAlign='center';
  const label=(h.label||'');
  const w=g.measureText(label).width+16;
  const ny=sy-116+bob;
  g.fillStyle='rgba(8,12,20,.78)';
  g.fillRect(sx-w/2,ny-14,w,22);
  g.strokeStyle=`rgba(240,213,144,${0.4+glow*0.4})`;g.lineWidth=1;g.strokeRect(sx-w/2,ny-14,w,22);
  g.fillStyle='#f0d590';g.fillText(label,sx,ny+2);
};
WMap.prototype.drawGate=function(g,gt,sx,sy,t){  const glow=0.5+0.5*Math.sin(t*2.2);
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
  // v1.9.62 按目标朝向翻转（立绘默认朝右，faceRight=false 时镜像）
  if(r.actor)r.actor.render(g,sx,sy,1,{dir:(r.faceRight===false?-1:1)});
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
      // v1.9.65 远端角色同样走 CLASS_BUNDLES（战士/骑士素材对调过，不能直接拼 'char_'+classId）
      const cid=(p.classId&&['warrior','mage','knight','fighter','sage','warlock'].indexOf(p.classId)>=0)?p.classId:'warrior';
      const rb=(window.CLASS_BUNDLES&&window.CLASS_BUNDLES[cid])||('char_'+cid);
      const actor=new this.StandeeActor(rb);
      actor.init().then(()=>actor.play('idle'));
      this.remote[p.id]={x:p.x,y:p.y,name:p.name,userId:p.userId||'',actor,faceRight:true};
    } else {
      const r=this.remote[p.id];
      // 插值：向目标坐标平滑移动（网络姿态平滑）
      const dx=p.x-r.x, dy=p.y-r.y;
      const moving=Math.hypot(dx,dy)>2;
      // v1.9.62 远端角色朝向：按服务器快照的位移方向决定，原来完全不翻转（永远朝同一个方向）
      if(moving&&Math.abs(dx)>0.5) r.faceRight=dx>0;
      r.x+=dx*0.3;r.y+=dy*0.3;
      r.name=p.name;r.userId=p.userId||'';
      // v1.9.62 远端角色走动时播 walk，停下回 idle（原来永远 idle，像在滑行）
      if(r.actor){
        if(moving&&r.actor.state!=='walk')r.actor.play('walk');
        else if(!moving&&r.actor.state!=='idle')r.actor.play('idle');
      }
    }
  }
  for(const id in this.remote)if(!seen[id])delete this.remote[id];
};

/* ---------- v1.9.64 地图旗标：点选地点 + 是否跟踪 ---------- */
/* 旗标只存本地（localStorage），不上报服务端：它不是玩法状态，只是玩家的导航便签。
   切图后旗标仍保留，但只在它所属的世界（wid）绘制，避免"在森林标的点出现在沙漠"。
   v1.9.82 支持多个标记点：id 唯一，各自独立 track；上限 PIN_MAX 防止满屏箭头。 */
const PIN_SVG='<svg class="pin-ico" viewBox="0 0 12 12" aria-hidden="true">'+
  '<path d="M2.2 11V1.2" stroke="#6b4c00" stroke-width="1.7"/>'+
  '<path d="M3 1.6 10.2 4.1 3 6.6Z" fill="#ffd65a" stroke="#6b4c00" stroke-width=".9"/></svg>';
const PIN_MAX=8;
let _pinSeq=0;
const newPinId=()=>'p'+Date.now().toString(36)+'_'+(++_pinSeq);
/* 当前世界的标记点（别的地图标的点不返回） */
WMap.prototype.worldPins=function(){
  if(!Array.isArray(this.pins))this.pins=[];
  return this.pins.filter(p=>p&&p.wid===this.worldId);
};
/* 新增标记点；超出上限返回 null（由调用方提示） */
WMap.prototype.addPin=function(x,y){
  if(this.worldPins().length>=PIN_MAX)return null;
  const p={id:newPinId(),wid:this.worldId,x:Math.round(x),y:Math.round(y),track:true};
  this.pins.push(p); this.savePins(); return p;
};
WMap.prototype.findPin=function(id){ return (this.pins||[]).find(p=>p.id===id)||null; };
WMap.prototype.removePin=function(id){
  this.pins=(this.pins||[]).filter(p=>p.id!==id); this.savePins();
};
WMap.prototype.clearPins=function(){
  const wid=this.worldId; this.pins=(this.pins||[]).filter(p=>p.wid!==wid); this.savePins();
};
/* 不传 id 时操作最近的那个（兼容旧调用） */
WMap.prototype.setPinTrack=function(on,id){
  const p=id?this.findPin(id):this.curPin();
  if(!p)return; p.track=!!on; this.savePins();
};
WMap.prototype.savePins=function(){
  try{ localStorage.setItem('zjcs.mapPins',JSON.stringify(this.pins||[])); }catch(e){}
  if(this._pinBarRefresh)this._pinBarRefresh();
};
/* 离角色最近的标记点（旧单旗标接口的等价物；地图条/小地图摘要用它） */
WMap.prototype.curPin=function(){
  const wp=this.worldPins(); if(!wp.length)return null;
  let best=wp[0],bd=Infinity;
  for(const p of wp){ const d=Math.hypot(p.x-this.hero.x,p.y-this.hero.y); if(d<bd){bd=d;best=p;} }
  return best;
};
/* 世界坐标 → 距离文案（1 格 = 160 世界单位，与探索格一致） */
WMap.prototype.pinDistOf=function(p){
  if(!p)return 0;
  return Math.round(Math.hypot(p.x-this.hero.x,p.y-this.hero.y)/160);
};
WMap.prototype.pinDist=function(){ return this.pinDistOf(this.curPin()); };
/* 旧签名兼容：setPin 现在等于"新增一个标记"，clearPin 等于"删掉最近的那个" */
WMap.prototype.setPin=function(x,y){ return this.addPin(x,y); };
WMap.prototype.clearPin=function(){ const p=this.curPin(); if(p)this.removePin(p.id); };

/* ---------- v1.9.68 主界面旗标方位提示（仅跟踪中） ----------
   两套画法按"旗标是否在视口内"切换：
   · 视口内 → 直接在地上画旗（旗杆 + 旗面 + 地面光环 + 向上的渐隐光柱），
     玩家看见的是"那个点在画面里"，距离贴在旗下沿。
   · 视口外 → 贴在屏幕边缘画方位箭头（v1.9.87）。早期版本因为怕被 HUD 压住，
     改成绕角色半径 64~78 画，结果远目标的方向感很弱：目标在正下方 20 格时，
     箭头停在画面中间，玩家以为就在附近，实际还要走很远。现在一律贴边，
     被面板盖住就盖住——层级本来就是地图之上、HUD 之下。 */
WMap.prototype.drawPinLabel=function(g,x,y,text,size){
  const fs=size||13, padX=Math.round(fs*0.72), h=Math.round(fs+7), r=h/2;
  g.save();
  g.font='700 '+fs+'px system-ui,-apple-system,"PingFang SC","Microsoft YaHei",sans-serif';
  g.textAlign='center'; g.textBaseline='middle';
  const w=g.measureText(text).width+padX*2;
  // 标签跟着箭头贴边时会被画布裁掉，这里统一夹进屏内（左右各留 4px，上下各留 2px）
  x=Math.min(Math.max(x,w/2+4),VW-w/2-4);
  y=Math.min(Math.max(y,h/2+2),VH-h/2-2);
  const l=x-w/2, tp=y-h/2, rt=x+w/2, bt=y+h/2;
  g.beginPath();
  g.moveTo(l+r,tp); g.arcTo(rt,tp,rt,bt,r); g.arcTo(rt,bt,l,bt,r);
  g.arcTo(l,bt,l,tp,r); g.arcTo(l,tp,rt,tp,r); g.closePath();
  g.fillStyle='rgba(8,12,20,.86)';
  g.strokeStyle='rgba(217,180,91,.92)'; g.lineWidth=fs>=16?1.8:1.2;
  g.fill(); g.stroke();
  g.fillStyle='#ffd65a'; g.fillText(text,x,y+0.5);
  g.restore();
};

WMap.prototype.drawPinHud=function(g,cx,cy,t){
  const hx=this.hero.x-cx, hy=this.hero.y-cy;
  // v1.9.89 每帧先清空箭头层，再只把"视口外"的箭头画到这一层上
  const eg=this.edgeG;
  if(eg)eg.clearRect(0,0,VW,VH);
  // 到达提示：不依赖 track（此刻已置 false），单独画成顶部横幅。
  // 不放角色头顶是因为 .interact-hint（"点击「xx」交战"）也贴在头顶，
  // 两者会正好叠在一起；顶部中央在 1280×720 下是空白区，不压小地图/任务栏/底部导航。
  if(this.pinToast){
    const left=this.pinToast.until-t;
    if(left<=0){ this.pinToast=null; }
    else{
      const k=Math.max(0,Math.min(1,left/3.0));     // 1 → 0
      g.save();
      g.globalAlpha=Math.min(1,k*3.5);               // 最后一段淡出
      this.drawPinLabel(g,VW/2,VH*0.16+(1-k)*14,this.pinToast.text,17);
      g.restore();
    }
  }
  const pins=this.worldPins().filter(p=>p.track);     // 只有跟踪中才提示，未跟踪不制造常驻噪音
  if(!pins.length)return;
  const dh=this.dockHeight?this.dockHeight():0;       // v1.9.90 底部黑条占位（每帧只读一次）
  const viewB=VH-dh;
  pins.forEach((p,i)=>{
    const px=p.x-cx, py=p.y-cy;
    const cells=this.pinDistOf(p);
    // 多个标记时前缀序号，和大地图/小地图上的编号一一对应
    const label=(pins.length>1?((i+1)+' · '):'')+(cells>0?(cells+' 格'):'就在附近');
    // ---- 视口内：地标旗（旗杆 + 旗面 + 地面光环 + 渐隐光柱） ----
    if(px>10&&px<VW-10&&py>34&&py<viewB-14){ this.drawPinFlag(g,px,py,t,label); return; }
    // ---- 视口外：画面边缘的方位箭头（闪烁） ----
    const ang=Math.atan2(p.y-this.hero.y,p.x-this.hero.x);
    const e=this.pinEdgePoint(hx,hy,ang,dh);
    // 优先画到独立箭头层（不被 HUD 盖住）；没有该层时退回主画布
    this.drawPinEdgeArrow(eg||g,e.x,e.y,ang,label,t);
  });
};

/* 视口内的地标旗：旗杆 + 旗面 + 地面呼吸光环 + 向上的渐隐光柱 */
WMap.prototype.drawPinFlag=function(g,px,py,t,label){
  const pul=0.5+0.5*Math.sin(t*3.2), bob=Math.sin(t*2.2)*2;
  const top=py-62+bob;
  g.save();
  // 光柱：先画，让旗杆压在上面
  const grd=g.createLinearGradient(px,top-26,px,py);
  grd.addColorStop(0,'rgba(255,214,90,0)');
  grd.addColorStop(1,'rgba(255,214,90,.34)');
  g.fillStyle=grd; g.fillRect(px-8,top-26,16,88);
  // 地面光环：深底 + 金环，保证在土黄/草地/石板上都能看清
  g.fillStyle='rgba(12,10,4,.20)';
  g.beginPath(); g.ellipse(px,py,22+pul*7,11+pul*3.5,0,0,Math.PI*2); g.fill();
  g.strokeStyle='rgba(255,224,120,'+(0.62+0.28*pul).toFixed(3)+')';
  g.lineWidth=2.6;
  g.beginPath(); g.ellipse(px,py,22+pul*7,11+pul*3.5,0,0,Math.PI*2); g.stroke();
  // 旗杆：深色打底 + 金色覆盖，顶端略高
  g.lineCap='round';
  g.strokeStyle='#231706'; g.lineWidth=4;
  g.beginPath(); g.moveTo(px,py); g.lineTo(px,top); g.stroke();
  g.strokeStyle='#ffd65a'; g.lineWidth=2;
  g.beginPath(); g.moveTo(px,py); g.lineTo(px,top); g.stroke();
  // 旗面：朝右的三角，深描边
  g.lineJoin='round';
  g.fillStyle='#ffd65a'; g.strokeStyle='#231706'; g.lineWidth=2;
  g.beginPath(); g.moveTo(px+1,top); g.lineTo(px+38,top+13); g.lineTo(px+1,top+26); g.closePath();
  g.fill(); g.stroke();
  g.restore();
  this.drawPinLabel(g,px,py+33,label);
};

/* 视口外标记点 → 画面边缘落点
   纯几何：从角色屏幕位置沿 atan2 方向射一条线，与屏幕内接矩形求交。
   内边距只留箭头圆底半径（17）+ 3px 余量，保证整个徽章都在屏内、紧贴边缘。

   v1.9.87 起不再读取 HUD 矩形、不再为了避开面板把箭头往画面中间挤：
   箭头画在 canvas 上，层级天然是「地图之上、HUD 之下」，被面板盖住可以接受，
   位置不准（明明目标在正下方，箭头却停在画面中间的底部导航上方）才是真问题。
   之前那套挤位逻辑会在底部导航/任务栏一带把箭头横向推几百像素，方向感全丢。 */
WMap.prototype.pinEdgePoint=function(hx,hy,ang,dh){
  const PAD=20;                       // 圆底半径 17 + 3
  // v1.9.90 底部黑条是新的可视边界：箭头贴在黑条上沿，不再落到黑条里压住导航按钮
  const L=PAD, R=VW-PAD, T=PAD, B=VH-(dh||0)-PAD;
  const ca=Math.cos(ang), sa=Math.sin(ang);
  let k=Infinity;
  if(ca>1e-6)k=Math.min(k,(R-hx)/ca); else if(ca<-1e-6)k=Math.min(k,(L-hx)/ca);
  if(sa>1e-6)k=Math.min(k,(B-hy)/sa); else if(sa<-1e-6)k=Math.min(k,(T-hy)/sa);
  if(!isFinite(k)||k<0)k=0;
  return {x:hx+ca*k, y:hy+sa*k};
};

/* 边缘方位箭头：深色圆底 + 金环 + 指向真实方位的三角，整体呼吸式闪烁 */
WMap.prototype.drawPinEdgeArrow=function(g,x,y,ang,label,t){
  const bl=0.52+0.48*Math.sin(t*4.6);                        // 闪烁：几乎全亮 ↔ 半透明
  const pu=0.5+0.5*Math.sin(t*4.6);
  g.save();
  // 外层扩散金光：让边缘这一点在余光里也能被注意到
  g.globalAlpha=0.16+0.30*pu;
  g.fillStyle='#ffd65a';
  g.beginPath(); g.arc(x,y,21+pu*9,0,Math.PI*2); g.fill();
  // 深色圆底 + 金环：任何地形/亮度下都清晰
  g.globalAlpha=0.60+0.36*bl;
  g.fillStyle='rgba(10,14,22,.88)';
  g.beginPath(); g.arc(x,y,17,0,Math.PI*2); g.fill();
  g.strokeStyle='rgba(255,214,90,.95)'; g.lineWidth=2;
  g.beginPath(); g.arc(x,y,17,0,Math.PI*2); g.stroke();
  // 三角箭头：旋转到真实方位（贴边后被 HUD 挤动位置时，方向依然正确）
  g.save(); g.translate(x,y); g.rotate(ang);
  g.fillStyle='#ffd65a'; g.strokeStyle='rgba(26,16,0,.95)'; g.lineWidth=1.6; g.lineJoin='round';
  g.beginPath(); g.moveTo(13,0); g.lineTo(-6,-8.5); g.lineTo(-2.6,0); g.lineTo(-6,8.5); g.closePath();
  g.fill(); g.stroke();
  g.restore();
  // 距离标签朝屏幕中心放，避免被画布边界裁掉
  const dx=VW/2-x, dy=VH/2-y, dd=Math.hypot(dx,dy)||1;
  g.globalAlpha=0.72+0.28*bl;
  this.drawPinLabel(g,x+dx/dd*38,y+dy/dd*38,label);
  g.restore();
};

/* ---------- v1.9.29 大地图弹窗（M 键开关 / Esc / ✕ / 遮罩点击关闭） ---------- */
WMap.prototype.toggleMapModal=function(){ if(this.mapModalOpen)this.closeMapModal(); else this.openMapModal(); };

WMap.prototype.openMapModal=function(){
  if(this.mapModalOpen)return;
  this.mapModalOpen=true;
  this.keys={}; this.hero.moving=false; // 打开瞬间停走
  // v1.9.46 顶部横幅已移除，标题优先取全局世界名（loadWorld 维护），最后回落到 worldId
  const title=window.__worldLabel||(this.worldId+' · 大世界');
  const layer=document.createElement('div');
  layer.id='wmapLayer';
  layer.innerHTML=`<div class="wmap-box">
    <div class="wmap-head"><b>${title}</b><span class="wmap-tip">点空地加标记 · 点标记开关跟踪 · 下方列表可删 · M / Esc 关闭</span>
      <button class="btn-gray" id="wmapOnline" style="margin-right:8px;padding:3px 12px">在线玩家</button>
      <button id="wmapClose" title="关闭 (Esc / M)">✕</button></div>
    <canvas id="wmapCv" width="960" height="540"></canvas>
    <div class="wmap-pinbar" id="wmapPinBar"></div>
    <div class="wmap-legend">
      <span><i class="lgdot lgdot-hero"></i>你</span>
      <span><i class="lgdot lgdot-gate"></i>传送门</span>
      <span><i class="lgdot lgdot-anchor"></i>锚点</span>
      <span><i class="lgdot lgdot-mob"></i>怪物</span>
      <span><i class="lgdot lgdot-elite"></i>精英/首领</span>
      <span><i class="lgdot lgdot-chest"></i>宝箱</span>
      <span><i class="lgdot lgdot-potion"></i>草药</span>
    </div>
  </div>`;
  // v1.9.47 挂到 #stage 内而非 body：#stage 带 transform 缩放，形成独立层叠上下文，
  // 挂在 body 上的地图层会永远压住 stage 内的所有弹窗（弹窗 z-index 再高也没用）。
  // 放进 stage 后与弹窗同一坐标系，层级大小关系才真正生效，同时也跟随舞台缩放。
  (document.getElementById('stage')||document.body).appendChild(layer);
  const self=this;
  layer.querySelector('#wmapClose').onclick=()=>self.closeMapModal();
  layer.querySelector('#wmapOnline').onclick=()=>{ self.opts.onOnlineList&&self.opts.onOnlineList(); };
  layer.addEventListener('mousedown',e=>{ if(e.target===layer)self.closeMapModal(); });
  // 底图兜底：当前世界缩略位图未就绪时再拉一张（家园用程序化整图兜底）
  if(!this.thumb){
    if(this.worldId==='HOME'&&this.groundHome){ this._mapThumbFallback=this.groundHome; }
    else{
      const img=new Image();
      img.src=RES+'maps/'+this.worldId+'_thumb.webp';
      this._mapThumbFallback=img;
    }
  }
  const isHome=this.worldId==='HOME';
  const legend=isHome
    ?`<span><i class="lgdot lgdot-hero"></i>你</span>
      <span><i class="lgdot lgdot-chest"></i>功能设施</span>
      <span><i class="lgdot lgdot-gate"></i>出门</span>`
    :`<span><i class="lgdot lgdot-hero"></i>你</span>
      <span><i class="lgdot lgdot-gate"></i>传送门</span>
      <span><i class="lgdot lgdot-anchor"></i>锚点</span>
      <span><i class="lgdot lgdot-mob"></i>怪物</span>
      <span><i class="lgdot lgdot-elite"></i>精英/首领</span>
      <span><i class="lgdot lgdot-chest"></i>宝箱</span>
      <span><i class="lgdot lgdot-potion"></i>草药</span>`;
  const cv=layer.querySelector('#wmapCv'), g=cv.getContext('2d');
  layer.querySelector('.wmap-legend').innerHTML=legend;

  /* v1.9.64 旗标交互；v1.9.82 支持多个标记点（列表 + 逐个删除） */
  const bar=layer.querySelector('#wmapPinBar');
  const refreshBar=()=>{
    const wp=self.worldPins();
    if(!wp.length){
      bar.innerHTML=`<span class="wmap-pin-none">${PIN_SVG}点地图任意位置放置标记点（最多 ${PIN_MAX} 个），点已有标记可开关跟踪</span>`;
      return;
    }
    let h=`<span class="wmap-pin-title">${PIN_SVG}标记 ${wp.length}/${PIN_MAX}</span>`;
    wp.forEach((p,i)=>{
      h+=`<span class="wmap-pin-chip${p.track?'':' off'}" data-id="${p.id}">`+
        `<i class="pc-idx">${i+1}</i>`+
        `<b class="pc-dist">${self.pinDistOf(p)} 格</b>`+
        `<button class="wmap-pin-btn pc-t${p.track?' on':''}" type="button" title="主界面边缘持续指向该点">${p.track?'跟踪中 ✓':'跟踪'}</button>`+
        `<button class="wmap-pin-btn ghost pc-x" type="button" title="删除这个标记点">✕</button>`+
      `</span>`;
    });
    h+=`<button class="wmap-pin-btn ghost" id="wmapPinAllClear" type="button">全部清除</button>`;
    bar.innerHTML=h;
    wp.forEach(p=>{
      const chip=bar.querySelector(`.wmap-pin-chip[data-id="${p.id}"]`);
      if(!chip)return;
      chip.querySelector('.pc-t').onclick=()=>{ self.setPinTrack(!p.track,p.id); refreshBar(); };
      chip.querySelector('.pc-x').onclick=()=>{ self.removePin(p.id); refreshBar(); };
    });
    bar.querySelector('#wmapPinAllClear').onclick=()=>{ self.clearPins(); refreshBar(); };
  };
  self._pinBarRefresh=refreshBar;
  refreshBar();
  cv.style.cursor='crosshair';
  // 屏幕坐标 → 世界坐标：canvas 用 width:100% 缩放，必须按 rect 比例换算，不能用 offsetX
  const pick=e=>{
    const r=cv.getBoundingClientRect(); if(!r.width||!r.height)return null;
    const S=cv.width/self.WM;
    const wx=((e.clientX-r.left)*cv.width/r.width)/S;
    const wy=((e.clientY-r.top)*cv.height/r.height)/S;
    if(wx<0||wy<0||wx>self.WM||wy>self.HM)return null;
    return {x:wx,y:wy};
  };
  // 命中已有标记？用来区分"新建"与"开关跟踪"
  const hitPin=w=>{
    const S=cv.width/self.WM, tol=15/S;
    return self.worldPins().find(p=>Math.hypot(p.x-w.x,p.y-w.y)<=tol)||null;
  };
  cv.addEventListener('click',e=>{
    const p=pick(e); if(!p)return;
    const hit=hitPin(p);
    if(hit){ self.setPinTrack(!hit.track,hit.id); }              // 点已有标记 → 开关跟踪
    else if(!self.addPin(p.x,p.y)){                              // 位置空 → 新建；满则提示
      self.pinToast={text:'标记点最多 '+PIN_MAX+' 个 · 先删掉一些',until:self.time+2.4};
    }
    refreshBar();
  });
  cv.addEventListener('mousemove',e=>{ self._pinHover=pick(e); });
  cv.addEventListener('mouseleave',()=>{ self._pinHover=null; });

  const loop=()=>{
    if(!this.mapModalOpen)return;
    this.drawMapModal(cv,g);
    this._mapRaf=requestAnimationFrame(loop);
  };
  loop();
};

WMap.prototype.drawMapModal=function(cv,g){
  const t=performance.now()/1000;
  g.clearRect(0,0,cv.width,cv.height);
  const img=this.thumb||this._mapThumbFallback;
  if(img&&(img.complete===undefined||img.complete))g.drawImage(img,0,0,cv.width,cv.height);
  else{ g.fillStyle='#101826'; g.fillRect(0,0,cv.width,cv.height); }
  const S=cv.width/this.WM; // v1.9.31 动态：常规世界 960/7680=1/8；家园 960/2560
  const P=(x,y)=>({x:x*S,y:y*S});
  const dot=(x,y,r,fill,stroke)=>{ g.beginPath(); g.arc(x,y,r,0,6.2832); g.fillStyle=fill; g.fill(); if(stroke){g.lineWidth=1.5;g.strokeStyle=stroke;g.stroke();} };
  // v1.9.64 悬停十字线：便于对准要标记的点（每帧重画，不落 DOM）
  if(this._pinHover){
    const h=P(this._pinHover.x,this._pinHover.y);
    g.save(); g.strokeStyle='rgba(255,214,90,.45)'; g.lineWidth=1; g.setLineDash([4,5]);
    g.beginPath(); g.moveTo(h.x,0); g.lineTo(h.x,cv.height); g.moveTo(0,h.y); g.lineTo(cv.width,h.y); g.stroke();
    g.restore();
  }
  // 怪物（精英/首领更大更醒目）
  for(const m of this.monsters){ const p=P(m.x,m.y); dot(p.x,p.y,m.elite?6:4,m.elite?'#e05a4a':'#c9756a','rgba(0,0,0,.5)'); }
  // 宝箱 / 草药（只画当前可交互的）
  for(const c of this.chests){ if(c.state!=='closed')continue; const p=P(c.x,c.y); dot(p.x,p.y,5,'#e0b455','#5a3c10'); }
  for(const pt of this.potions){ if(pt.state!=='ready')continue; const p=P(pt.x,pt.y); dot(p.x,pt.y,4,'#5fd38a','#1d5a34'); }
  // 锚点 / 传送门
  for(const a of this.anchors){ const p=P(a.x,a.y); dot(p.x,p.y,5,'#6fa8dc','#20415e'); }
  for(const gt of this.gates){ const p=P(gt.x,gt.y);
    const pulse=3+Math.sin(t*3)*1.2;
    dot(p.x,p.y,6+pulse,'rgba(150,110,220,.28)');
    dot(p.x,p.y,6,'#a97fe8','#3d2470');
  }
  // v1.9.31 家园功能点（金色方点）
  for(const h of this.homeItems){ const p=P(h.x,h.y);
    g.fillStyle='#e0b455';g.fillRect(p.x-4,p.y-4,8,8);
    g.strokeStyle='#5a3c10';g.lineWidth=1.5;g.strokeRect(p.x-4,p.y-4,8,8);
  }
  // 主角：金色脉冲
  const hp=P(this.hero.x,this.hero.y);
  dot(hp.x,hp.y,9+Math.sin(t*2.5)*2.5,'rgba(255,214,90,.25)');
  dot(hp.x,hp.y,6,'#ffd65a','#6b4c00');
  // v1.9.64 旗标：金色小旗 + 距离；跟踪开启时画角色→旗标的虚线
  // v1.9.82 多个标记点：编号与下方列表一致，未跟踪的画灰色（一眼看出哪些在导航）
  const wp=this.worldPins();
  wp.forEach((pin,i)=>{
    const pp=P(pin.x,pin.y);
    const gold=pin.track;
    if(pin.track){
      g.save(); g.strokeStyle='rgba(255,214,90,.62)'; g.lineWidth=2.6; g.setLineDash([8,6]);
      g.beginPath(); g.moveTo(hp.x,hp.y); g.lineTo(pp.x,pp.y); g.stroke(); g.restore();
    }
    // 落点光晕（脉动）：小旗在满屏地形里容易被忽略，用一圈呼吸光勾出来
    dot(pp.x,pp.y,10+Math.sin(t*2.2+i)*2.5,gold?'rgba(255,214,90,.2)':'rgba(150,168,190,.18)');
    g.save();
    g.lineCap='round';
    // 旗杆：深棕描边 + 亮金内芯，深色草地/雪地上都能看清
    g.strokeStyle=gold?'#6b4c00':'#2a3442'; g.lineWidth=4.2;
    g.beginPath(); g.moveTo(pp.x,pp.y); g.lineTo(pp.x,pp.y-26); g.stroke();
    g.strokeStyle=gold?'#ffe9a8':'#9fb0c6'; g.lineWidth=2.2;
    g.beginPath(); g.moveTo(pp.x,pp.y); g.lineTo(pp.x,pp.y-26); g.stroke();
    // 旗面朝内：紧贴右边缘时翻到左边，不然旗子会被画布切掉一半
    const fs=pp.x>cv.width-46?-1:1;
    g.fillStyle=gold?'#ffd65a':'#8fa0b8'; g.strokeStyle=gold?'#6b4c00':'#2a3442'; g.lineWidth=1.8; g.lineJoin='round';
    g.beginPath(); g.moveTo(pp.x+fs*1,pp.y-26); g.lineTo(pp.x+fs*22,pp.y-18.5); g.lineTo(pp.x+fs*1,pp.y-11); g.closePath();
    g.fill(); g.stroke();
    g.restore();
    // 距离牌：多个标记时带上序号，和列表/小地图对应；贴边时往内推，避免被画布裁掉
    const dm=this.pinDistOf(pin), txt=(wp.length>1?(i+1)+' · ':'')+dm+' 格';
    g.save(); g.font='bold 13px sans-serif'; g.textAlign='center';
    const tw=g.measureText(txt).width;
    const bx=Math.min(Math.max(pp.x,tw/2+10),cv.width-tw/2-10);
    const by=Math.min(pp.y+8,cv.height-26);
    g.fillStyle='rgba(8,12,20,.78)'; g.fillRect(bx-tw/2-6,by,tw+12,18);
    g.strokeStyle=gold?'rgba(217,180,91,.5)':'rgba(120,140,165,.42)'; g.lineWidth=1; g.strokeRect(bx-tw/2-6,by,tw+12,18);
    g.fillStyle=gold?'#ffd65a':'#9fb0c6'; g.fillText(txt,bx,by+13);
    g.restore();
  });
};

WMap.prototype.closeMapModal=function(){
  if(!this.mapModalOpen)return;
  this.mapModalOpen=false;
  if(this._mapRaf)cancelAnimationFrame(this._mapRaf);
  this._pinHover=null; this._pinBarRefresh=null; // 弹窗内引用随 DOM 一并释放
  const l=document.getElementById('wmapLayer'); if(l)l.remove();
};

window.WorldMap=WMap;
window.WorldMapConst={MW,MH,VW,VH,THEMES,MINI_W,MINI_H,MINI_SCALE,THUMB_W,THUMB_H,THUMB_SCALE};
})();

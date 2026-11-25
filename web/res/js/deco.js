/* ============================================================
   杖剑传说 · 地表装饰层（v1.9 视觉重建）
   ------------------------------------------------------------
   目标：把"通道 + 纯色深林"的占位画面，补成有层次的像素森林。
   - 1:1 像素细节（1 世界单位 = 1 canvas 像素）
   - 全部程序化像素图案（无外部资源、无网络请求、随主题换色）
   - 按 1280×720 分块惰性烘焙（与地形块同网格），LRU 上限 20 块
   - 确定性随机：同一块每次生成完全一致，切回世界不会"换脸"
   层次：林缘密林（大树 + 阴影）→ 林内远树（暗色）→ 路边草簇/花/石/土粒
   ============================================================ */
(function(){
'use strict';

/* ---------- 像素图案（字符画） ----------
   0/透明 · 1 暗 · 2 中 · 3 亮 · 4 树干 · 5 花心 · 6 点缀色 */
const P={
  tree:[
    '00000000333300000000',
    '00000033222233000000',
    '00003332222223330000',
    '00033222222222233000',
    '00332222222222222330',
    '03322223332222332223',
    '03222233333322333222',
    '32222333333333333322',
    '32223333333333333222',
    '32223333333333332222',
    '32222333333333322222',
    '03322233333333222223',
    '03322223333332222230',
    '00332222222222222330',
    '00033222222222233300',
    '00003322222222330000',
    '00000033222233000000',
    '00000000044000000000',
    '00000000044000000000',
    '00000000044000000000'
  ],
  treeRound:[
    '0000003333000000',
    '0000332222330000',
    '0003322222233000',
    '0033222222223300',
    '0332222222222330',
    '0322211111222230',
    '0322111111112230',
    '3322111111112233',
    '3222111111111223',
    '3222111111111223',
    '3322111111112233',
    '0322111111112230',
    '0322211111222230',
    '0033222222223300',
    '0000004400000000',
    '0000004400000000'
  ],
  pine:[
    '000000033330000000',
    '000000322223000000',
    '000003222222300000',
    '000032222222230000',
    '000322112221223000',
    '003221111221122300',
    '032211111111112230',
    '322111111111111223',
    '000003222222300000',
    '000032211112230000',
    '000322111111223000',
    '003221111111122300',
    '032211111111112230',
    '322111111111111223',
    '000000004400000000',
    '000000004400000000',
    '000000004400000000'
  ],
  rock:[
    '0000000000000000',
    '0000003333000000',
    '0000332222330000',
    '0003322222233000',
    '0032211111122300',
    '0322111111112230',
    '3221111111111223',
    '3211111111111123',
    '3221111111111223',
    '0322211111122230',
    '0033222222223300',
    '0000332222330000',
    '0000003333000000',
    '0000000000000000'
  ],
  spire:[
    '......66......',
    '.....6556.....',
    '.....6556.....',
    '....655556....',
    '....655556....',
    '...65555556...',
    '...65555556...',
    '..6555555556..',
    '..6555555556..',
    '..6555555556..',
    '.655555555556.',
    '.655555555556.',
    '.655555555556.',
    '.655555555556.'
  ],
  bush:[
    '000000000',
    '000333000',
    '003222300',
    '032212230',
    '322111223',
    '321111123',
    '322111223',
    '003222300',
    '000333000'
  ],
  flower:[
    '.....5.....',
    '....353....',
    '...35553...',
    '....353....',
    '.....4.....',
    '....44.....',
    '.....4.....'
  ],
  stone:[
    '..........',
    '...3333...',
    '..322223..',
    '.32211223.',
    '.32111123.',
    '.32211223.',
    '..322223..',
    '...3333...'
  ],
  mushroom:[
    '....66....',
    '..666666..',
    '.66566566.',
    '6666666666',
    '.66666666.',
    '....44....',
    '....44....',
    '...4444...'
  ],
  reed:[
    '..3...3..',
    '.3.3.3.3.',
    '.3.3.3.3.',
    '..3.3.3..',
    '..3.3.3..',
    '...3.3...',
    '...3.3...',
    '....4....'
  ],
  crystal:[
    '....66....',
    '...6556...',
    '..655556..',
    '.65555556.',
    '.65555556.',
    '..655556..',
    '...6556...',
    '....66....'
  ]
};

/* 主题调色板 */
const PAL={
  grassland:{1:'#1f4423',2:'#2f6330',3:'#44803d',4:'#3a2a18',5:'#f7e9a8',6:'#e8a0b8',
             g1:'#6fa63f',g2:'#84c356',g3:'#a2dd6b',rock:'#8d959d',rockL:'#bcc6cd',
             dirt:'#7a6243',dirtL:'#9c8055'},
  forest:   {1:'#14301a',2:'#204526',3:'#2f5c35',4:'#2c1f12',5:'#f0d590',6:'#d98a8a',
             g1:'#4b7a3c',g2:'#5d9150',g3:'#74ad62',rock:'#77828a',rockL:'#a3aeb5',
             dirt:'#6b5c44',dirtL:'#8a7a5e'},
  desert:   {1:'#6d4c2b',2:'#8f6539',3:'#b98a4e',4:'#5c3f22',5:'#f0d590',6:'#e08a5a',
             g1:'#b08a52',g2:'#c69a5c',g3:'#dcb273',rock:'#a2845f',rockL:'#d0ac82',
             dirt:'#a9834f',dirtL:'#c9a16f'},
  snow:     {1:'#31505f',2:'#436b7c',3:'#e6f2fa',4:'#5e5142',5:'#dff2ff',6:'#8fd0ff',
             g1:'#8fa9b8',g2:'#b3c9d6',g3:'#d6e6ef',rock:'#96a8b5',rockL:'#cfdde7',
             dirt:'#b9c9d4',dirtL:'#dde9f0'},
  cave:     {1:'#4f4436',2:'#6a5c46',3:'#87765c',4:'#402f1f',5:'#c8b58a',6:'#8a7a63',
             g1:'#6f6049',g2:'#82725a',g3:'#9a876c',rock:'#887c6b',rockL:'#b2a48d',
             dirt:'#7d6d55',dirtL:'#9c8a6e'},
  arena:    {1:'#241f36',2:'#37314d',3:'#544c6c',4:'#1d1929',5:'#f0d590',6:'#9ad8ff',
             g1:'#3a3450',g2:'#4a4560',g3:'#5f5878',rock:'#5f5a75',rockL:'#8b86a5',
             dirt:'#4d4762',dirtL:'#6b6482'}
};

function mulberry32(a){
  return function(){
    a|=0; a=a+0x6D2B79F5|0;
    let t=Math.imul(a^a>>>15,1|a);
    t=t+Math.imul(t^t>>>7,61|t)^t;
    return ((t^t>>>14)>>>0)/4294967296;
  };
}

/* 绘制字符画图案（像素块 cell 对齐到整数，避免亚像素缝） */
function stamp(g,rows,x,y,cell,colorOf){
  const ic=Math.max(1,Math.round(cell));
  for(let ry=0;ry<rows.length;ry++){
    const line=rows[ry];
    for(let rx=0;rx<line.length;rx++){
      const ch=line[rx];
      if(ch==='0'||ch==='.')continue;
      g.fillStyle=colorOf(ch);
      g.fillRect(x+rx*ic,y+ry*ic,ic,ic);
    }
  }
}
/* 树：阴影 + 图案 + 树干，size=整体像素高 */
function drawTree(g,P0,pal,x,y,size,kind,flip,dark){
  const pat=kind===2?P.pine:(kind===1?P.treeRound:P.tree);
  const ic=Math.max(1,Math.round(size/pat.length));
  // 落地阴影：树冠越大阴影越宽，压住林下地面，形成纵深
  g.fillStyle='rgba(10,24,12,'+(dark?0.42:0.34)+')';
  g.beginPath();g.ellipse(x+ic*4,y+ic*1.5,ic*pat[0].length*0.42,ic*2.2,0,0,Math.PI*2);g.fill();
  const w=pat[0].length;
  const x0=Math.round(x-ic*w/2);
  const y0=Math.round(y-size+ic*1.5);
  const colorOf=ch=>{
    if(ch==='4')return dark?'#33240f':pal[4];
    if(dark&&ch==='3')return pal[2];
    if(dark&&ch==='2')return pal[1];
    return pal[ch]||pal[3];
  };
  if(flip){
    for(let ry=0;ry<pat.length;ry++){
      const line=pat[ry];
      for(let rx=0;rx<w;rx++){
        const ch=line[w-1-rx];
        if(ch==='0'||ch==='.')continue;
        g.fillStyle=colorOf(ch);
        g.fillRect(x0+rx*ic,y0+ry*ic,ic,ic);
      }
    }
  }else{
    stamp(g,pat,x0,y0,ic,colorOf);
  }
}
/* 草簇：暗底 + 亮尖，避免在草地上"看不见" */
function drawGrassTuft(g,rnd,pal,x,y){
  const n=4+Math.floor(rnd()*4);
  for(let i=0;i<n;i++){
    const h=9+Math.floor(rnd()*12);
    const gx=Math.round(x+(rnd()-0.5)*16), gy=Math.round(y);
    g.fillStyle=pal.g1;
    g.fillRect(gx,gy-h,2,h);
    g.fillStyle=pal.g3;
    g.fillRect(gx,gy-h,1,Math.max(3,h-3));
    if(rnd()<0.5){ g.fillStyle=pal.g2; g.fillRect(gx+3,gy-h+3,1,h-4); }
  }
  g.fillStyle='rgba(20,40,20,.18)';
  g.fillRect(Math.round(x-5),Math.round(y),10,2);
}

function DecoLayer(worldId,themeKey){
  this.worldId=worldId||'W1';
  this.themeKey=themeKey||'grassland';
  this.pal=PAL[themeKey]||PAL.grassland;
  this.tiles=new Map();
  this.max=20;
}
DecoLayer.prototype.clear=function(){ this.tiles.clear(); };
DecoLayer.prototype.distanceToWalkable=function(walk,x,y,maxR){
  for(let r=18;r<=maxR;r+=18){
    for(let a=0;a<8;a++){
      const ang=a*Math.PI/4;
      if(walk(x+Math.cos(ang)*r,y+Math.sin(ang)*r))return r;
    }
  }
  return maxR+1;
};

DecoLayer.prototype.bake=function(tx,ty,VW,VH,walk,isWater,isPaved){
  const cv=document.createElement('canvas');
  cv.width=VW; cv.height=VH;
  const g=cv.getContext('2d');
  g.imageSmoothingEnabled=false;
  const ox=tx*VW, oy=ty*VH;
  const rnd=mulberry32(((tx*73856093)^(ty*19349663)^((this.worldId.charCodeAt(1)||1)*83492791))>>>0);
  const pal=this.pal, th=this.themeKey;
  // 石板广场不长草：铺装区只保留碎石，避免"石砖缝里冒草丛"的违和
  const isPv=(wx,wy)=>isPaved?isPaved(wx,wy):false;

  /* ---- 1) 林缘与林内：树 ---- */
  // 网格抖动采样：越靠近通道越密，形成"森林墙"，边缘由树冠自然压住通道直角
  const trees=[];
  const GS=52;
  for(let gy=-GS;gy<VH+GS;gy+=GS){
    for(let gx=-GS;gx<VW+GS;gx+=GS){
      const x=gx+(rnd()-0.5)*GS*0.8, y=gy+(rnd()-0.5)*GS*0.8;   // 块内局部坐标（绘制用）
      const wx=ox+x, wy=oy+y;                                     // 世界坐标（判定用）
      if(walk(wx,wy))continue;                                    // 通道内不种树
      if(isWater&&isWater(wx,wy))continue;                        // 水面不长树
      const dist=this.distanceToWalkable(walk,wx,wy,96);
      const edge=dist<=30, near=dist<=96;
      const p=edge?0.92:near?0.55:0.3;
      if(rnd()>p)continue;
      const size=edge?(104+rnd()*56):near?(92+rnd()*46):(78+rnd()*36);
      const kind=rnd()<0.62?0:(rnd()<0.55?1:2);
      const flip=rnd()<0.5;
      const dark=dist>44;
      if(th==='desert'){
        // 沙漠：风化岩堆（宽底、分层、带投影），比"细柱"更像峡谷地貌
        const sz=size*0.72;
        g.fillStyle='rgba(30,18,10,.28)';
        g.beginPath();g.ellipse(x+sz*0.14,y+sz*0.06,sz*0.5,sz*0.16,0,0,Math.PI*2);g.fill();
        stamp(g,P.rock,Math.round(x-sz*0.5),Math.round(y-sz*0.8),Math.max(2,Math.round(sz/16)),
              ch=>ch==='1'?pal[1]:ch==='2'?pal[2]:pal[3]);
      }else if(th==='arena'){
        // 星陨：发光晶簇
        const sz=size*0.8;
        g.fillStyle='rgba(120,180,255,.10)';
        g.beginPath();g.ellipse(x,y-sz*0.3,sz*0.55,sz*0.55,0,0,Math.PI*2);g.fill();
        g.fillStyle='rgba(10,8,20,.35)';
        g.beginPath();g.ellipse(x+sz*0.1,y+sz*0.04,sz*0.4,sz*0.13,0,0,Math.PI*2);g.fill();
        stamp(g,P.spire,Math.round(x-sz*0.42),Math.round(y-sz*0.86),Math.max(2,Math.round(sz/16)),
              ch=>ch==='6'?'#7fb6e8':ch==='5'?'#3f4f78':pal[ch]);
      }else{
        trees.push({x,y,size,kind,flip,dark});
      }
    }
  }

  // 路缘：沿可走/不可走交界撒草簇与碎石，柔化掩码放大的直角
  const ES=22;
  for(let y=-ES;y<VH+ES;y+=ES){
    for(let x=-ES;x<VW+ES;x+=ES){
      const px=x+(rnd()-0.5)*ES, py=y+(rnd()-0.5)*ES;
      const wx=ox+px, wy=oy+py;
      if(!walk(wx,wy))continue;
      if(isWater&&isWater(wx,wy))continue;
      if(isPv(wx,wy))continue;
      let edge=false;
      for(let a=0;a<8;a++){
        const ang=a*Math.PI/4;
        if(!walk(wx+Math.cos(ang)*26,wy+Math.sin(ang)*26)){edge=true;break;}
      }
      if(!edge||rnd()<0.32)continue;
      if(rnd()<0.86)drawGrassTuft(g,rnd,pal,px,py);
      else stamp(g,P.stone,Math.round(px),Math.round(py-8),3,ch=>ch==='1'?'#6f767d':ch==='2'?pal.rock:'#c2cad1');
    }
  }

  trees.sort((a,b)=>a.y-b.y);
  for(const t of trees)drawTree(g,P,pal,t.x,t.y,t.size,t.kind,t.flip,t.dark);

  /* ---- 2) 通道表面：草簇 / 花 / 石子 / 土粒 ---- */
  const grassN=Math.round((th==='desert'||th==='snow')?22:58);
  for(let i=0;i<grassN;i++){
    const x=rnd()*VW, y=rnd()*VH;
    if(!walk(ox+x,oy+y))continue;
    if(isPv(ox+x,oy+y))continue;
    drawGrassTuft(g,rnd,pal,x,y);
  }
  // 土粒：让 4px 噪点路面有更细的颗粒层次
  for(let i=0;i<150;i++){
    const x=Math.round(rnd()*VW), y=Math.round(rnd()*VH);
    if(!walk(ox+x,oy+y))continue;
    g.fillStyle=rnd()<0.5?pal.dirt:'rgba(255,255,255,.06)';
    g.fillRect(x,y,1,1);
    if(rnd()<0.25)g.fillRect(x+1,y+1,2,1);
  }
  const flowerN=th==='desert'?2:th==='snow'?1:9;
  for(let i=0;i<flowerN;i++){
    const x=rnd()*VW, y=rnd()*VH;
    if(!walk(ox+x,oy+y))continue;
    if(isPv(ox+x,oy+y))continue;
    stamp(g,P.flower,Math.round(x),Math.round(y-14),3,ch=>pal[ch]||pal[5]);
  }
  for(let i=0;i<6;i++){
    const x=rnd()*VW, y=rnd()*VH;
    if(!walk(ox+x,oy+y))continue;
    if(isPv(ox+x,oy+y))continue;      // 石板上不放浅色碎石（会变成扎眼白点）
    const sc=rnd()<0.35?2:1;
    stamp(g,P.stone,Math.round(x),Math.round(y-10),3*sc,ch=>ch==='1'?'#6f767d':ch==='2'?pal.rock:'#c2cad1');
  }
  if(th==='forest'||th==='cave'){
    for(let i=0;i<5;i++){
      const x=rnd()*VW,y=rnd()*VH;
      if(!walk(ox+x,oy+y))continue;
      if(isPv(ox+x,oy+y))continue;
      stamp(g,P.mushroom,Math.round(x),Math.round(y-12),3,ch=>pal[ch]);
    }
  }
  if(th==='desert'||th==='arena'){
    for(let i=0;i<3;i++){
      const x=rnd()*VW,y=rnd()*VH;
      if(!walk(ox+x,oy+y))continue;
      if(isPv(ox+x,oy+y))continue;
      stamp(g,P.crystal,Math.round(x),Math.round(y-14),3,ch=>pal[ch]);
    }
  }
  /* ---- 2b) 铺装边缘：石板与草地的接缝撒杂草/碎石，柔化圆形广场的硬边 ---- */
  if(isPaved){
    for(let i=0;i<44;i++){
      const x=rnd()*VW, y=rnd()*VH;
      const wx=ox+x, wy=oy+y;
      if(!walk(wx,wy))continue;
      if(isWater&&isWater(wx,wy))continue;
      const here=isPaved(wx,wy)?1:0;
      const near=isPaved(wx+38,wy)!==here||isPaved(wx,wy+38)!==here||
                 isPaved(wx-38,wy)!==here||isPaved(wx,wy-38)!==here;
      if(!near)continue;
      if(here){ if(rnd()<0.62)drawGrassTuft(g,rnd,pal,x,y); }
      else if(rnd()<0.30)stamp(g,P.stone,Math.round(x),Math.round(y-10),3,ch=>ch==='1'?'#6f767d':ch==='2'?pal.rock:'#c2cad1');
    }
  }
  /* ---- 3) 水边芦苇 ---- */
  if(th==='grassland'||th==='forest'||th==='cave'){
    for(let i=0;i<12;i++){
      const x=rnd()*VW, y=rnd()*VH;
      const wx=ox+x, wy=oy+y;
      if(!walk(wx,wy))continue;
      if(this.distanceToWalkable(walk,wx,wy,60)<20)continue;       // 太贴墙的跳过
      let nearWall=false;
      for(let a=0;a<8;a++){
        const ang=a*Math.PI/4;
        if(!walk(wx+Math.cos(ang)*46,wy+Math.sin(ang)*46)){nearWall=true;break;}
      }
      if(!nearWall)continue;
      stamp(g,P.reed,Math.round(x),Math.round(y-22),2,ch=>pal[ch]||pal.g3);
    }
  }
  return cv;
};
DecoLayer.prototype.get=function(tx,ty,VW,VH,walk,isWater,isPaved){
  const key=tx+','+ty;
  let c=this.tiles.get(key);
  if(c){ this.tiles.delete(key); this.tiles.set(key,c); return c; }
  c=this.bake(tx,ty,VW,VH,walk,isWater,isPaved);
  this.tiles.set(key,c);
  while(this.tiles.size>this.max){ this.tiles.delete(this.tiles.keys().next().value); }
  return c;
};

window.DecoLayer=DecoLayer;
window.DecoPalette=PAL;
})();

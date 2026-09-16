// Measures the things "professional, plain, elegant" actually reduce to, so the
// claim can be checked instead of asserted: how much page there is, whether it
// overflows, how many distinct typographic and colour values are in play (a
// design system has few; an accretion has many), and whether any text fails
// WCAG AA against the background it is actually painted on.
const {chromium} = require('playwright');
const PAGES = JSON.parse(process.env.PAGES || '[["/","overview"]]');
const WIDTHS = JSON.parse(process.env.WIDTHS || '[390,1440]');
const BASE = process.env.BASE || 'http://127.0.0.1:3111';

const lum = (r,g,b) => { const f=c=>{c/=255;return c<=0.03928?c/12.92:Math.pow((c+0.055)/1.055,2.4)};
  return 0.2126*f(r)+0.7152*f(g)+0.0722*f(b); };
const ratio = (a,b) => { const [x,y]=[lum(...a),lum(...b)].sort((p,q)=>q-p); return (x+0.05)/(y+0.05); };

(async () => {
  const b = await chromium.launch({executablePath:'/opt/pw-browsers/chromium'});
  const out = [];
  for (const [url, name] of PAGES) {
    for (const w of WIDTHS) {
      const p = await b.newPage({viewport:{width:w,height:1000}});
      await p.goto(BASE+url, {waitUntil:'networkidle'});
      await p.waitForTimeout(2000);
      const m = await p.evaluate(() => {
        const parse = s => { const m=/rgba?\(([\d.]+),\s*([\d.]+),\s*([\d.]+)(?:,\s*([\d.]+))?\)/.exec(s||'');
          return m ? [+m[1],+m[2],+m[3], m[4]===undefined?1:+m[4]] : null; };
        const bgOf = el => { let n=el; while(n){ const c=parse(getComputedStyle(n).backgroundColor);
            if(c && c[3]>0.95) return c.slice(0,3); n=n.parentElement; } return [255,255,255]; };
        const sizes=new Set(), weights=new Set(), colors=new Set(), fams=new Set(), radii=new Set();
        const pairs=[];
        for (const el of document.querySelectorAll('body *')) {
          const cs = getComputedStyle(el);
          if (cs.display==='none' || cs.visibility==='hidden') continue;
          const txt=[...el.childNodes].some(n=>n.nodeType===3 && n.textContent.trim());
          sizes.add(cs.fontSize); weights.add(cs.fontWeight); fams.add(cs.fontFamily.split(',')[0].trim());
          if (cs.borderRadius!=='0px') radii.add(cs.borderRadius);
          if (txt) { colors.add(cs.color);
            const fg=parse(cs.color); if(fg) pairs.push({fg:fg.slice(0,3), bg:bgOf(el),
              size:parseFloat(cs.fontSize), weight:+cs.fontWeight,
              sample:(el.textContent||'').trim().slice(0,40)}); }
        }
        return {height:document.documentElement.scrollHeight,
          overflow:document.documentElement.scrollWidth-innerWidth,
          tables:[...document.querySelectorAll('table')].map(t=>t.scrollWidth),
          sizes:[...sizes], weights:[...weights], colors:[...colors], fams:[...fams],
          radii:[...radii], pairs};
      });
      const fails = m.pairs.filter(q => {
        const need = (q.size>=24 || (q.size>=18.66 && q.weight>=700)) ? 3 : 4.5;
        return ratio(q.fg,q.bg) < need;
      });
      const uniqFails = [...new Map(fails.map(f=>[f.fg+''+f.bg, {...f, r:+ratio(f.fg,f.bg).toFixed(2)}])).values()];
      out.push({page:name, width:w, height:m.height, overflow:m.overflow,
        widestTable:Math.max(0,...m.tables),
        fontSizes:m.sizes.length, fontWeights:m.weights.length, textColors:m.colors.length,
        families:m.fams.length, radii:m.radii.length,
        largest:Math.max(...m.sizes.map(parseFloat)),
        contrastFailures:uniqFails.length,
        worst:uniqFails.sort((a,c)=>a.r-c.r).slice(0,4)});
      await p.close();
    }
  }
  console.log(JSON.stringify(out,null,1));
  await b.close();
})().catch(e=>{console.error(e);process.exit(1)});

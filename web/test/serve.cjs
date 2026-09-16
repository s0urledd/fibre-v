const http=require('http'),fs=require('fs'),path=require('path');
const ROOT=process.env.ROOT||require('path').join(__dirname,'..','out'), API=Number(process.env.API_PORT||8099), PORT=Number(process.env.PORT||3111);
const types={'.html':'text/html','.js':'text/javascript','.css':'text/css','.json':'application/json','.svg':'image/svg+xml','.txt':'text/plain','.ico':'image/x-icon','.woff2':'font/woff2'};
http.createServer((req,res)=>{
  const u=new URL(req.url,'http://x');
  if(u.pathname.startsWith('/api/')){
    const p=http.request({host:'127.0.0.1',port:API,path:u.pathname.slice(4)+u.search,method:req.method},r=>{res.writeHead(r.statusCode,r.headers);r.pipe(res)});
    p.on('error',e=>{res.writeHead(502);res.end(String(e))}); req.pipe(p); return;
  }
  let f=path.join(ROOT,u.pathname);
  if(!fs.existsSync(f)||fs.statSync(f).isDirectory()){
    const alt=f.replace(/\/$/,'')+'.html'; f=fs.existsSync(alt)?alt:path.join(f,'index.html');
  }
  if(!fs.existsSync(f)){res.writeHead(404);res.end('nf');return}
  res.writeHead(200,{'content-type':types[path.extname(f)]||'application/octet-stream'});
  fs.createReadStream(f).pipe(res);
}).listen(PORT,()=>console.log('serving '+ROOT+' on '+PORT+' -> api '+API));

import { chromium } from '@playwright/test';
import { existsSync } from 'node:fs';
import { mkdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';
import { languages, pagePath } from '../src/lib/languages.mjs';
import { API, REPO, parseRelease, selectDownload } from '../src/lib/releases.mjs';
const root=path.resolve(process.env.SITE_SCRATCH || '../../.tmp/mm-site/site-verify');await mkdir(root,{recursive:true});
const base=process.env.SITE_URL || 'http://127.0.0.1:53982';
const axe=await readFile(new URL('../node_modules/axe-core/axe.min.js',import.meta.url),'utf8');
const assets=[];
for(const os of ['linux','windows','darwin'])for(const arch of ['amd64','arm64'])for(const format of ({linux:['deb','rpm','tar.gz'],windows:['msi','zip'],darwin:['dmg','tar.gz']})[os]){
 const name=`spk-mm-client_1.0.0_${os}_${arch}.${format}`;
 for(const suffix of ['', '.sha256'])assets.push({name:name+suffix,browser_download_url:`${REPO}/releases/download/v1.0.0/${name+suffix}`,size:1024*1024});
}
assets.reverse();
const fixture={tag_name:'v1.0.0',assets};
const release=parseRelease(fixture);
const browser=await chromium.launch({...(existsSync('/usr/bin/google-chrome')?{executablePath:'/usr/bin/google-chrome'}:{}),headless:true,args:['--no-sandbox']});
const uas={windows:'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/140.0.0.0 Safari/537.36',linux:'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/140.0.0.0 Safari/537.36',darwin:'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/140.0.0.0 Safari/537.36'};
async function prepare(context,hints){await context.addInitScript(hints=>{Object.defineProperty(navigator,'userAgentData',{value:hints?{getHighEntropyValues:async()=>hints}:undefined,configurable:true});},hints);const page=await context.newPage();await page.route(API,r=>r.fulfill({json:fixture}));return page;}
async function ready(page){await page.locator('[data-download-controls]').waitFor({state:'visible'});}
async function check(page,os,arch){const file=selectDownload(release,os,arch);assert(file);for(const button of await page.locator('[data-download-label]').all()){await page.waitForFunction(({element,url})=>element.href===url,{element:await button.elementHandle(),url:file.url});assert.match(await button.getAttribute('aria-label'),new RegExp(file.format.toUpperCase()));}return file;}
try{
 for(const {code} of languages)for(const colorScheme of ['light','dark'])for(const width of [375,768,1440]){
  const context=await browser.newContext({viewport:{width,height:1000},colorScheme,userAgent:uas.windows,locale:'en-US'});
  try{
   const page=await prepare(context,{architecture:'x86',bitness:'64'});
   await page.goto(base+pagePath(code,'/spk-mm-client/'));await ready(page);await page.evaluate(()=>document.fonts.ready);await page.evaluate(()=>Promise.all(document.getAnimations().map(animation=>animation.finished.catch(()=>{}))));
   await check(page,'windows','amd64');
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   await page.evaluate(axe);const a=await page.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(a.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[]);
   if(code==='ru'||code==='en')await page.screenshot({path:path.join(root,`automatic-${code}-${colorScheme}-${width}.png`),fullPage:true});
   console.log('PASS automatic download',code,colorScheme,width,'MSI link, label, layout, WCAG AA');
  }finally{await context.close();}
 }
 for(const os of ['linux','windows','darwin'])for(const arch of ['amd64','arm64']){
  const context=await browser.newContext({userAgent:uas[os],locale:'en-US'});try{
   const page=await prepare(context,{architecture:arch==='arm64'?'arm':'x86',bitness:'64'});await page.goto(base+'/spk-mm-client/en/');await ready(page);const file=await check(page,os,arch);
   await page.route(file.url,r=>r.fulfill({headers:{'content-type':'application/octet-stream','content-disposition':`attachment; filename="${file.name}"`},body:'download fixture'}));
   const downloadPromise=page.waitForEvent('download');await page.locator('.hero-actions [data-download-label]').click();const download=await downloadPromise;assert.equal(download.suggestedFilename(),file.name);assert.equal(new URL(page.url()).pathname,'/spk-mm-client/en/');
   console.log('PASS detected platform download',os,arch,file.format);
  }finally{await context.close();}
 }
 const context=await browser.newContext({userAgent:uas.darwin,locale:'en-US'});try{
  const page=await prepare(context,null);await page.goto(base+'/spk-mm-client/en/');await ready(page);
  assert.equal(await page.locator('select[name=os]').inputValue(),'darwin');assert.equal(await page.locator('select[name=arch]').inputValue(),'');
  assert.equal(await page.locator('.hero-actions [data-download-label]').getAttribute('href'),'#downloads');
  await page.locator('select[name=arch]').selectOption('arm64');await check(page,'darwin','arm64');
  await page.locator('select[name=os]').selectOption('windows');await check(page,'windows','arm64');
  console.log('PASS ambiguous Mac requires architecture; manual selectors update primary buttons');
 }finally{await context.close();}
 const mobile=await browser.newContext({userAgent:'Mozilla/5.0 (Linux; Android 15; Mobile)',locale:'en-US'});try{
  const page=await prepare(mobile,{architecture:'arm',bitness:'64'});await page.goto(base+'/spk-mm-client/en/');await ready(page);
  assert.equal(await page.locator('.hero-actions [data-download-label]').getAttribute('href'),'#downloads');assert.equal(await page.locator('select[name=os]').inputValue(),'');
  console.log('PASS mobile keeps platform choice instead of downloading a Linux installer');
 }finally{await mobile.close();}
}finally{await browser.close();}

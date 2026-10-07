import {chromium} from '@playwright/test';
import assert from 'node:assert/strict';
import {readFile,mkdir} from 'node:fs/promises';
import {existsSync} from 'node:fs';
import path from 'node:path';
import {languages,pagePath} from '../src/lib/languages.mjs';
const root=path.resolve(process.env.SITE_SCRATCH||'../../.tmp/mm-site/site-verify');await mkdir(root,{recursive:true});
const base=process.env.SITE_URL||'http://127.0.0.1:53982';const API='https://api.github.com/repos/Sipaha/spk-mm-client/releases/latest';
const axe=await readFile(new URL('../node_modules/axe-core/axe.min.js',import.meta.url),'utf8');
const browser=await chromium.launch({...(existsSync('/usr/bin/google-chrome')?{executablePath:'/usr/bin/google-chrome'}:{}),headless:true,args:['--no-sandbox']});
async function audit(page){await page.evaluate(axe);const result=await page.evaluate(async()=>await window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(result.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[]);}
try{
 for(const {code} of languages) for(const theme of ['light','dark']) for(const width of [375,768,1440]){
  const ctx=await browser.newContext({viewport:{width,height:1000},colorScheme:theme,locale:'en-US'});try{
   const page=await ctx.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));await page.route(API,r=>r.fulfill({status:404,json:{message:'No release'}}));
   await page.goto(base+pagePath(code,'/spk-mm-client/'));await page.evaluate(()=>document.fonts.ready);
   assert.equal(await page.locator('html').getAttribute('lang'),code);assert.equal(await page.locator('html').getAttribute('data-theme'),theme);
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   assert.equal(await page.locator('a[href="https://github.com/Sipaha/spk-mm-client/blob/main/LICENSE"]').count(),1);
   assert.equal(await page.locator(`a[href="https://sipaha.github.io/about/${code==='ru'?'?lang=ru':code+'/'}"]`).count(),2);
   await page.waitForFunction(()=>document.querySelector('[data-release-status]').textContent!==JSON.parse(document.querySelector('[data-downloads]').dataset.labels).loading);
   assert.equal(await page.locator('a[href*="/releases/download/"]').count(),0);
   for(const image of await page.locator('img:visible').all()){await image.scrollIntoViewIfNeeded();await page.waitForFunction(e=>e.complete&&e.naturalWidth>0,await image.elementHandle());}
   for(const tab of await page.locator('[role=tab]').all()){
    await tab.click();const panel=page.locator('[role=tabpanel]:visible');assert.equal(await panel.count(),1);const img=panel.locator('img');await img.scrollIntoViewIfNeeded();await page.waitForFunction(e=>e.complete&&e.naturalWidth>0,await img.elementHandle());
   }
   await page.locator('[role=tab]').last().press('Home');assert.equal(await page.locator('[role=tab]').first().getAttribute('aria-selected'),'true');
   await audit(page);
   await page.locator('.header-actions a[href="#downloads"]').click();await page.locator('#downloads').waitFor();
   await page.screenshot({path:path.join(root,`${code}-${theme}-${width}-downloads.png`)});
   await page.evaluate(()=>{document.activeElement?.blur();document.documentElement.style.scrollBehavior='auto';scrollTo(0,0);});await page.waitForFunction(()=>scrollY===0);await page.screenshot({path:path.join(root,`${code}-${theme}-${width}.png`),fullPage:true});assert.deepEqual(errors,[]);
   console.log('PASS',code,theme,width,'layout,images,links,no-release,gallery,WCAG AA');
  }finally{await ctx.close();}
 }
 for(const mode of ['unavailable','valid-release','denied-storage','reduced-motion','language']){
  const ctx=await browser.newContext({viewport:{width:375,height:900},locale:'en-US',reducedMotion:'reduce'});try{
   if(mode==='denied-storage')await ctx.addInitScript(()=>{Object.defineProperty(window,'localStorage',{get(){throw new DOMException('Denied','SecurityError');}});});
   const page=await ctx.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
   const url='https://github.com/Sipaha/spk-mm-client/releases/download/v1.0.0/spk-mm-client_1.0.0_linux_amd64.deb';
   await page.route(API,r=>mode==='unavailable'?r.fulfill({status:503,json:{message:'Unavailable'}}):mode==='valid-release'?r.fulfill({json:{tag_name:'v1.0.0',assets:[{name:'spk-mm-client_1.0.0_linux_amd64.deb',browser_download_url:url},{name:'spk-mm-client.exe',browser_download_url:'https://foreign.invalid/app.exe'}]}}):r.fulfill({status:404,json:{}}));
   await page.goto(base+'/spk-mm-client/en/');await page.waitForFunction(()=>document.querySelector('[data-release-status]').textContent!==JSON.parse(document.querySelector('[data-downloads]').dataset.labels).loading);
   if(mode==='unavailable')assert.match(await page.locator('[data-release-status]').innerText(),/Could not load/);
   if(mode==='valid-release'){assert.equal(await page.locator(`a[href="${url}"]`).count(),1);assert.equal(await page.locator('a[href*="foreign.invalid"]').count(),0);await audit(page);}
   if(mode==='denied-storage'){await page.locator('.theme-toggle').click();assert.equal(await page.locator('html').getAttribute('data-theme'),'dark');}
   if(mode==='reduced-motion')assert.equal(await page.locator('.hero-heading').evaluate(el=>getComputedStyle(el).animationName),'none');
   if(mode==='language'){await page.locator('.language-menu summary').click();await page.locator('[data-language=de]').click();await page.waitForURL('**/de/');assert.equal(await page.locator('html').getAttribute('lang'),'de');await page.locator('.language-menu summary').click();await page.locator('[data-language=ru]').click();await page.waitForURL('**/?lang=ru');assert.equal(await page.locator('html').getAttribute('lang'),'ru');}
   assert.deepEqual(errors,[]);console.log('PASS',mode);
  }finally{await ctx.close();}
 }
 const ctx=await browser.newContext({javaScriptEnabled:false,viewport:{width:375,height:900}});try{const p=await ctx.newPage();await p.goto(base+'/spk-mm-client/en/');assert(await p.locator('a[href*="README.md"]').count()>0);assert(await p.locator('a[href$="/releases"]').count()>0);assert(await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));console.log('PASS no JavaScript fallback');}finally{await ctx.close();}
 // Social cards are unmodified captures of the real product website.
 for(const code of (process.env.GENERATE_SOCIAL_CARDS==='1'?['en','ru']:[])){const ctx=await browser.newContext({viewport:{width:1200,height:630},colorScheme:'light',reducedMotion:'reduce'});try{const p=await ctx.newPage();await p.route(API,r=>r.fulfill({status:404,json:{}}));await p.goto(base+pagePath(code,'/spk-mm-client/'));await p.evaluate(()=>document.fonts.ready);await p.locator('.hero img').evaluate(e=>e.decode());await p.screenshot({path:path.resolve(`public/media/og-${code}.png`)});}finally{await ctx.close();}}
}finally{await browser.close();}

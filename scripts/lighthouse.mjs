import { chromium } from '@playwright/test';
import lighthouse from 'lighthouse';
import { writeFile,mkdir } from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';
const root=path.resolve(process.env.SITE_SCRATCH||'../../.tmp/mm-site/site-verify');await mkdir(root,{recursive:true});
const browser=await chromium.launch({executablePath:'/usr/bin/google-chrome',headless:true,args:['--no-sandbox','--remote-debugging-port=53985']});
try{for(const formFactor of ['mobile','desktop']){
 const settings=formFactor==='desktop'?{formFactor:'desktop',screenEmulation:{mobile:false,width:1440,height:900,deviceScaleFactor:1,disabled:false}}:{};
 const result=await lighthouse((process.env.SITE_URL||'http://127.0.0.1:53982')+'/spk-mm-client/en/',{port:53985,output:'json',logLevel:'error',onlyCategories:['performance','accessibility','best-practices','seo'],...settings});
 await writeFile(path.join(root,`lighthouse-${formFactor}.json`),result.report);
 const scores=Object.fromEntries(Object.entries(result.lhr.categories).map(([k,v])=>[k,Math.round(v.score*100)]));console.log(formFactor,scores,'LCP',result.lhr.audits['largest-contentful-paint'].numericValue,'CLS',result.lhr.audits['cumulative-layout-shift'].numericValue);
 assert(scores.performance>=90);assert.equal(scores.accessibility,100);assert(scores['best-practices']>=95);assert(scores.seo>=95);assert(result.lhr.audits['cumulative-layout-shift'].numericValue<.1);
}}finally{await browser.close();}

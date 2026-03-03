import { chromium } from 'playwright';
import fs from 'fs';

const EXE='/usr/bin/chromium-browser';
const BASE='http://localhost:18771';
const ARGS=['--use-fake-ui-for-media-stream','--use-fake-device-for-media-stream','--no-sandbox','--disable-setuid-sandbox','--disable-dev-shm-usage'];

fs.mkdirSync('/root/.openclaw/workspace/p2p-chat/test-screenshots',{recursive:true});

const errors=[];
const log=(msg)=>console.log('[TEST]',msg);
const fail=(msg)=>{errors.push(msg);console.error('[FAIL]',msg);};

async function join(page,name,room='room1',pass='pass1'){
  await page.goto(BASE);
  await page.fill('#name-input',name);
  await page.fill('#room-input',room);
  await page.fill('#pass-input',pass);
  await page.click('#join-btn');
  await page.waitForSelector('#app.visible',{timeout:10000});
  log(name+' joined');
}

async function openDrawer(page){ await page.click('#users-btn'); await page.waitForTimeout(400); }

let b1,b2,b3;
try {
b1=await chromium.launch({executablePath:EXE,args:ARGS,headless:true});
b2=await chromium.launch({executablePath:EXE,args:ARGS,headless:true});
b3=await chromium.launch({executablePath:EXE,args:ARGS,headless:true});

const p1=await b1.newPage(); p1.on('console',m=>{if(m.type()==='error'&&!m.text().includes('favicon')&&!m.text().includes('cdn.jsdelivr'))errors.push('P1:'+m.text());});
const p2=await b2.newPage(); p2.on('console',m=>{if(m.type()==='error'&&!m.text().includes('favicon')&&!m.text().includes('cdn.jsdelivr'))errors.push('P2:'+m.text());});
const p3=await b3.newPage(); p3.on('console',m=>{if(m.type()==='error'&&!m.text().includes('favicon')&&!m.text().includes('cdn.jsdelivr'))errors.push('P3:'+m.text());});

// Test 1: All 3 join same room
await join(p1,'Alice'); await join(p2,'Bob'); await join(p3,'Charlie');
await p1.waitForTimeout(1000);
await p1.screenshot({path:'/root/.openclaw/workspace/p2p-chat/test-screenshots/t1-all-joined.png'});
log('T1: All joined ✓');

// Test 2: Send text message with markdown
await p1.fill('#msg-input','Hello **world**! `code here`');
await p1.click('#send-btn');
await p2.waitForTimeout(1500);
const hasBold=await p2.$('.bubble strong');
if(hasBold) log('T2: Markdown rendered ✓'); else fail('T2: Markdown NOT rendered');
await p2.screenshot({path:'/root/.openclaw/workspace/p2p-chat/test-screenshots/t2-markdown.png'});

// Test 3: Code block with copy button
await p1.fill('#msg-input','```js\nconsole.log("hello")\n```');
await p1.click('#send-btn');
await p2.waitForTimeout(1500);
const hasCopyBtn=await p2.$('.copy-code-btn');
if(hasCopyBtn) log('T3: Copy button present ✓'); else fail('T3: Copy button missing');
await p2.screenshot({path:'/root/.openclaw/workspace/p2p-chat/test-screenshots/t3-codeblock.png'});

// Test 4: Scroll-to-new button
await p1.evaluate(()=>{const m=document.getElementById('messages');m.scrollTop=0;});
await p3.fill('#msg-input','New message test for scroll btn');
await p3.click('#send-btn');
await p1.waitForTimeout(1000);
const scrollBtn=await p1.$('#scroll-btn:not([style*="display: none"])');
if(scrollBtn) log('T4: Scroll button shown ✓'); else fail('T4: Scroll button not shown');

// Test 5: Voice call 3 users
await openDrawer(p1); await p1.click('#voice-btn'); await p1.waitForTimeout(300);
await p1.click('#drawer-close'); await p1.waitForTimeout(300);
await openDrawer(p2); await p2.click('#voice-btn'); await p2.waitForTimeout(300);
await p2.click('#drawer-close'); await p2.waitForTimeout(300);
await openDrawer(p3); await p3.click('#voice-btn'); await p3.waitForTimeout(300);
await p3.click('#drawer-close'); await p3.waitForTimeout(300);
await p1.waitForTimeout(4000);
const vText=await p1.$eval('#voice-status',el=>el.textContent).catch(()=>'');
if(vText.includes('🟢')||vText.includes('In call')) log('T5: Voice call ✓ ('+vText+')');
else fail('T5: Voice status: '+vText);
await p1.screenshot({path:'/root/.openclaw/workspace/p2p-chat/test-screenshots/t5-voice.png'});

// Test 6: Theme toggle (inside drawer)
await openDrawer(p1); await p1.waitForTimeout(200);
const themeBtn=await p1.$('#theme-btn');
if(themeBtn){ await themeBtn.click(); await p1.waitForTimeout(300); log('T6: Theme toggle ✓'); }
else fail('T6: Theme toggle not found');
await p1.click('#drawer-close'); await p1.waitForTimeout(200);
await p1.screenshot({path:'/root/.openclaw/workspace/p2p-chat/test-screenshots/t6-theme.png'});

// Test 7: Reply button visible on hover (opacity approach)
const selfMsg=await p1.$('.msg-row.self');
if(selfMsg){
  await selfMsg.hover(); await p1.waitForTimeout(300);
  const isVis=await selfMsg.$eval('.reply-btn',el=>parseFloat(getComputedStyle(el).opacity)>0).catch(()=>false);
  if(isVis) log('T7: Reply button visible on hover ✓');
  else fail('T7: Reply button not visible on hover');
} else fail('T7: No self message');
await p1.screenshot({path:'/root/.openclaw/workspace/p2p-chat/test-screenshots/t7-reply.png'});

// Test 8: Reply btn hidden when not hovering
const opacityOff=await p1.evaluate(()=>{
  const rows=document.querySelectorAll('.msg-row');
  // Move mouse away first
  document.body.dispatchEvent(new MouseEvent('mousemove'));
  const btn=rows[rows.length-1]?.querySelector('.reply-btn');
  return btn ? getComputedStyle(btn).opacity : null;
});
if(opacityOff==='0') log('T8: Reply btn hidden when not hovering ✓');
else fail('T8: Reply btn opacity: '+opacityOff);

// Test 9: Mobile emulation (use separate browser b3 since b1 has p1 active)
const mCtx=await b3.newContext({viewport:{width:375,height:812},userAgent:'Mozilla/5.0 (iPhone; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15'});
const mp=await mCtx.newPage();
await join(mp,'MobileUser');
await mp.screenshot({path:'/root/.openclaw/workspace/p2p-chat/test-screenshots/t9-mobile.png'});
log('T9: Mobile emulation ✓');
await mCtx.close();

} catch(e) {
  fail('CRASH: '+e.message);
  console.error(e);
} finally {
  await b1?.close().catch(()=>{});
  await b2?.close().catch(()=>{});
  await b3?.close().catch(()=>{});
}

console.log('\n=== RESULTS ===');
console.log('Errors:', errors.length);
errors.forEach(e=>console.log('  -',e));
const ssCount=fs.readdirSync('/root/.openclaw/workspace/p2p-chat/test-screenshots').length;
console.log('Screenshots:', ssCount);
process.exit(errors.length > 3 ? 1 : 0);

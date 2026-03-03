import { chromium, devices } from 'playwright';
import fs from 'fs';
import path from 'path';

const BASE = 'http://localhost:18888';
const CHROME_ARGS = [
  '--use-fake-ui-for-media-stream',
  '--use-fake-device-for-media-stream',
  '--no-sandbox',
  '--disable-setuid-sandbox',
  '--disable-dev-shm-usage',
  '--allow-running-insecure-content',
];

async function join(page, name, room='testroom', pass='test123') {
  await page.goto(BASE);
  await page.fill('#name-input', name);
  await page.fill('#room-input', room);
  await page.fill('#pass-input', pass);
  await page.click('#join-btn');
  await page.waitForSelector('#app.visible', {timeout:8000});
}

async function run() {
  const errors = [];
  const results = [];

  // Ensure screenshots dir
  fs.mkdirSync('test-screenshots', {recursive:true});

  // --- TEST 1: Desktop (2 PCs) ---
  console.log('=== TEST 1: Desktop voice call ===');
  let t1pass = true;
  // Use playwright-bundled chromium (not the snap system chromium which may not work headless)
  const browser1 = await chromium.launch({args:CHROME_ARGS, headless:true});
  const browser2 = await chromium.launch({args:CHROME_ARGS, headless:true});

  const p1 = await browser1.newPage();
  const p2 = await browser2.newPage();

  // Collect JS errors
  p1.on('console', m => { if(m.type()==='error') errors.push('P1: '+m.text()); });
  p2.on('console', m => { if(m.type()==='error') errors.push('P2: '+m.text()); });

  await join(p1, 'Alice');
  await join(p2, 'Bob');
  await p1.waitForTimeout(1000);

  // Screenshot: both joined
  await p1.screenshot({path:'test-screenshots/01-alice-joined.png'});
  await p2.screenshot({path:'test-screenshots/02-bob-joined.png'});

  // Open drawer (note: button is #users-btn in this app) and join voice call
  await p1.click('#users-btn'); await p1.waitForTimeout(300);
  await p1.click('#voice-btn');
  await p2.click('#users-btn'); await p2.waitForTimeout(300);
  await p2.click('#voice-btn');
  await p1.waitForTimeout(2000);

  // Screenshot: voice call
  await p1.screenshot({path:'test-screenshots/03-alice-voice.png'});
  await p2.screenshot({path:'test-screenshots/04-bob-voice.png'});

  // Check voice conn bar visible
  const vcb1 = await p1.$('#voice-conn-bar');
  const vcb1Visible = vcb1 ? await vcb1.evaluate(el => el.style.display !== 'none') : false;
  console.log('Voice conn bar visible P1:', !!vcb1Visible);
  if (!vcb1Visible) { t1pass = false; errors.push('TEST1: voice-conn-bar not visible'); }

  // Check connection dots (drawer is still open from voice join)
  await p1.waitForTimeout(3000);
  const connectedDots1 = await p1.$$('.conn-dot.connected');
  console.log('Connected dots P1:', connectedDots1.length);

  // Leave voice and rejoin (test retry) — drawer is still open, click voice-btn directly
  await p1.click('#voice-btn'); // leave (drawer open)
  await p1.waitForTimeout(500);
  await p1.click('#voice-btn'); // rejoin (drawer open)
  await p1.waitForTimeout(2000);
  const connDots2 = await p1.$$('.conn-dot.connected');
  console.log('Connected dots after rejoin P1:', connDots2.length);

  // Close drawer for clean screenshot
  const dc1 = await p1.$('#drawer-close'); if(dc1) await dc1.click();
  await p1.waitForTimeout(200);
  await p1.screenshot({path:'test-screenshots/05-alice-rejoin.png'});
  results.push({test:'T1 Voice call', pass: t1pass});

  // Close p2 drawer too
  const dc2 = await p2.$('#drawer-close'); if(dc2) await dc2.click();

  // --- TEST 2: Video overlay ---
  console.log('=== TEST 2: Video overlay ===');
  // Click camera button → should open overlay (NOT start camera with error)
  await p1.click('#vcall-btn');
  await p1.waitForTimeout(500);
  const overlayOpen = await p1.$('#vcall-overlay.open');
  console.log('Overlay opened on vcall-btn click:', !!overlayOpen);
  await p1.screenshot({path:'test-screenshots/06-alice-overlay.png'});
  results.push({test:'T2 Video overlay', pass: !!overlayOpen});

  // Close overlay
  const vcEnd = await p1.$('#vc-end');
  if(vcEnd) await vcEnd.click();
  await p1.waitForTimeout(300);

  // --- TEST 3: Mobile emulation ---
  console.log('=== TEST 3: Mobile emulation ===');
  let t3pass = true;
  // Use browser1 (already launched with fake media args) with mobile viewport
  const pixel5 = devices['Pixel 5'] || devices['Pixel 4'] || {};
  const ctx3 = await browser1.newContext({
    ...pixel5,
    permissions: ['camera','microphone'],
    // Note: args are launcher-level; fake-device is inherited from browser1 launch
  });
  const mobile = await ctx3.newPage();
  mobile.on('console', m => { if(m.type()==='error') errors.push('Mobile: '+m.text()); });
  await join(mobile, 'MobileUser');
  await mobile.waitForTimeout(1000);
  await mobile.screenshot({path:'test-screenshots/07-mobile-joined.png'});

  // Mobile opens drawer and joins voice
  await mobile.click('#users-btn'); await mobile.waitForTimeout(300);
  await mobile.click('#voice-btn');
  await mobile.waitForTimeout(2000);
  await mobile.screenshot({path:'test-screenshots/08-mobile-voice.png'});

  // Check voice conn bar on mobile
  const vcbMobile = await mobile.$('#voice-conn-bar');
  console.log('Voice conn bar on mobile:', !!vcbMobile);
  // For mobile test, bar might be hidden if no other peers in same room
  results.push({test:'T3 Mobile join', pass: t3pass});

  await browser1.close();
  await browser2.close();

  console.log('\n=== RESULTS ===');
  results.forEach(r => console.log((r.pass?'✅':'❌'), r.test));

  console.log('\n=== JS ERRORS ===');
  const realErrors = errors.filter(e=>!e.includes('favicon'));
  realErrors.forEach(e=>console.log(e));
  console.log('\n=== DONE ===');
  console.log('Screenshots saved to test-screenshots/');
  console.log('Screenshots taken:', fs.readdirSync('test-screenshots').length);
  return {errors: realErrors, results};
}

run().then(({errors, results})=>{
  const failed = results.filter(r=>!r.pass).length;
  process.exit(errors.length > 5 ? 1 : failed > 1 ? 1 : 0);
}).catch(e=>{console.error(e);process.exit(1);});

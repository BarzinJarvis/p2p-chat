import { chromium } from 'playwright';
import path from 'path';
import { fileURLToPath } from 'url';
const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.join(__dirname, '..');
const ARGS = ['--use-fake-ui-for-media-stream','--use-fake-device-for-media-stream','--no-sandbox','--disable-setuid-sandbox','--disable-dev-shm-usage'];
const BASE = 'http://127.0.0.1:18769';
const SS = p => path.join(ROOT, 'test-screenshots', p);

async function join(page, name) {
  await page.goto(BASE, {timeout:15000});
  await page.fill('#room-input', 'smoke3');
  await page.fill('#pass-input', 'pw123');
  await page.fill('#name-input', name);
  await page.click('#join-btn');
  await page.waitForSelector('#app.visible', { timeout: 10000 });
}

const R = [];
function ok(n, v) { R.push({n, v}); console.log(v ? `✅ ${n}` : `❌ ${n}`); }

try {
  const b1 = await chromium.launch({ args: ARGS, headless: true });
  const b2 = await chromium.launch({ args: ARGS, headless: true });
  const p1 = await b1.newPage();
  const p2 = await b2.newPage();

  await join(p1, 'Alice');
  ok('Alice joins', true);
  await join(p2, 'Bob');
  ok('Bob joins', true);
  await p2.waitForTimeout(500);

  // messaging
  await p1.fill('#msg-input', 'Hello Bob!');
  await p1.click('#send-btn');
  await p2.waitForSelector('.msg-row.other .bubble-text', { timeout: 5000 });
  const msg = await p2.$eval('.msg-row.other .bubble-text', el => el.textContent);
  ok('Message delivery', msg === 'Hello Bob!');

  // theme toggle
  await p1.click('#users-btn');
  await p1.waitForTimeout(200);
  await p1.click('#theme-btn');
  const theme = await p1.$eval('html', el => el.dataset.theme);
  ok('Theme toggle to light', theme === 'light');
  await p1.screenshot({ path: SS('v3-light-theme.png') });
  await p1.click('#theme-btn');
  await p1.click('#drawer-close');

  // scroll button
  for (let i = 0; i < 25; i++) {
    await p2.fill('#msg-input', `M${i}`);
    await p2.click('#send-btn');
    await p2.waitForTimeout(30);
  }
  await p1.waitForTimeout(600);
  await p1.evaluate(() => document.getElementById('messages').scrollTop = 0);
  await p1.waitForTimeout(200);
  await p2.fill('#msg-input', 'Trigger!');
  await p2.click('#send-btn');
  await p1.waitForTimeout(800);
  const sb = await p1.$eval('#scroll-btn', el => el.style.display !== 'none');
  ok('Scroll button visible', sb);
  if (sb) {
    await p1.click('#scroll-btn');
    await p1.waitForTimeout(300);
    const sbHidden = await p1.$eval('#scroll-btn', el => el.style.display === 'none');
    ok('Scroll button hides on click', sbHidden);
  }
  await p1.screenshot({ path: SS('v3-scroll.png') });

  // voice call
  await p1.click('#users-btn');
  await p1.waitForTimeout(200);
  await p1.click('#voice-btn');
  await p1.waitForTimeout(500);
  const vs = await p1.$eval('#voice-status', el => el.textContent);
  ok('Voice call active', vs.includes('In call'));
  await p1.screenshot({ path: SS('v3-voice.png') });
  await p1.click('#drawer-close');

  // disconnect overlay exists  
  const discExists = await p1.$('#disc-overlay');
  ok('Disconnect overlay HTML present', !!discExists);

  // edit preview exists
  const epExists = await p1.$('#edit-preview');
  ok('Edit preview HTML present', !!epExists);

  await p1.screenshot({ path: SS('v3-alice-final.png') });
  await p2.screenshot({ path: SS('v3-bob-final.png') });

  await b1.close();
  await b2.close();
} catch (e) {
  console.error('ERR:', e.message);
  ok('Unhandled', false);
}

const p = R.filter(r => r.v).length;
console.log(`\n${p}/${R.length} passed`);
process.exit(p === R.length ? 0 : 1);

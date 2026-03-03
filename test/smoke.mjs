import { chromium } from 'playwright';
import { spawn } from 'child_process';
import path from 'path';
import { fileURLToPath } from 'url';
const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.join(__dirname, '..');
const ARGS = ['--use-fake-ui-for-media-stream','--use-fake-device-for-media-stream','--no-sandbox','--disable-setuid-sandbox','--disable-dev-shm-usage'];
const BASE = 'http://localhost:18769';
const EXE = '/usr/bin/chromium-browser';
const SS = p => path.join(ROOT, 'test-screenshots', p);

const srv = spawn(path.join(ROOT, 'chat-linux-amd64'), [], { env: { ...process.env, PORT: '18769' }, cwd: ROOT });
await new Promise(r => setTimeout(r, 1500));

async function join(page, name) {
  await page.goto(BASE);
  await page.fill('#room-input', 'smoke');
  await page.fill('#pass-input', 'pw123');
  await page.fill('#name-input', name);
  await page.click('#join-btn');
  await page.waitForSelector('#app.visible', { timeout: 8000 });
}

const results = [];
function check(name, ok) { results.push({ name, ok }); console.log(ok ? `✅ ${name}` : `❌ ${name}`); }

try {
  const b1 = await chromium.launch({ executablePath: EXE, args: ARGS, headless: true });
  const b2 = await chromium.launch({ executablePath: EXE, args: ARGS, headless: true });
  const p1 = await b1.newPage();
  const p2 = await b2.newPage();

  // Test join
  await join(p1, 'Alice');
  check('Alice joins', true);
  await join(p2, 'Bob');
  check('Bob joins', true);
  await p2.waitForTimeout(500);

  // Test messaging
  await p1.fill('#msg-input', 'Hello Bob!');
  await p1.click('#send-btn');
  await p2.waitForSelector('.msg-row.other .bubble-text', { timeout: 5000 });
  const msg = await p2.$eval('.msg-row.other .bubble-text', el => el.textContent);
  check('Message delivery', msg === 'Hello Bob!');

  // Test theme toggle
  await p1.click('#users-btn');
  await p1.waitForTimeout(200);
  await p1.click('#theme-btn');
  const theme = await p1.$eval('html', el => el.dataset.theme);
  check('Theme toggle', theme === 'light');
  await p1.click('#theme-btn'); // back to dark
  await p1.click('#drawer-close');

  // Test scroll button
  for (let i = 0; i < 20; i++) {
    await p2.fill('#msg-input', `Msg${i}`);
    await p2.click('#send-btn');
    await p2.waitForTimeout(50);
  }
  await p1.waitForTimeout(500);
  await p1.evaluate(() => document.getElementById('messages').scrollTop = 0);
  await p1.waitForTimeout(200);
  await p2.fill('#msg-input', 'Trigger scroll btn');
  await p2.click('#send-btn');
  await p1.waitForTimeout(800);
  const scrollBtn = await p1.$eval('#scroll-btn', el => el.style.display !== 'none');
  check('Scroll button appears', scrollBtn);
  await p1.screenshot({ path: SS('smoke-scroll-btn.png') });

  // Test voice call (2 users)
  await p1.click('#users-btn');
  await p1.waitForTimeout(200);
  await p1.click('#voice-btn');
  await p1.waitForTimeout(500);
  await p1.click('#drawer-close');
  
  await p2.click('#users-btn');
  await p2.waitForTimeout(200);
  await p2.click('#voice-btn');
  await p2.waitForTimeout(500);
  await p2.click('#drawer-close');
  
  await p1.waitForTimeout(3000);
  await p1.click('#users-btn');
  await p1.waitForTimeout(200);
  const voiceStatus = await p1.$eval('#voice-status', el => el.textContent);
  check('Voice call active', voiceStatus.includes('In call'));
  await p1.screenshot({ path: SS('smoke-voice.png') });
  await p1.click('#drawer-close');

  // Screenshot final state
  await p1.screenshot({ path: SS('smoke-alice-final.png') });
  await p2.screenshot({ path: SS('smoke-bob-final.png') });

  await b1.close();
  await b2.close();
} catch (e) {
  console.error('Test error:', e.message);
  check('Unhandled error', false);
}

srv.kill();
const passed = results.filter(r => r.ok).length;
const total = results.length;
console.log(`\n${'═'.repeat(40)}\n${passed}/${total} tests passed\n${'═'.repeat(40)}`);
process.exit(passed === total ? 0 : 1);

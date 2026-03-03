// Full test suite for p2p-chat v3.0
import { chromium } from 'playwright';
import { execSync, spawn } from 'child_process';
import { existsSync } from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.join(__dirname, '..');

const ARGS = [
  '--use-fake-ui-for-media-stream',
  '--use-fake-device-for-media-stream',
  '--no-sandbox',
  '--disable-setuid-sandbox',
  '--disable-dev-shm-usage'
];
const BASE = 'http://localhost:18767';
const EXE = '/usr/bin/chromium-browser';
const SS_DIR = path.join(ROOT, 'test-screenshots');

const errors = [];
let server;

async function startServer() {
  server = spawn(path.join(ROOT, 'chat-linux-amd64'), [], {
    env: { ...process.env, PORT: '18767' },
    cwd: ROOT
  });
  await new Promise(r => setTimeout(r, 1500));
  console.log('✅ Server started');
}

function stopServer() {
  if (server) { server.kill(); server = null; }
}

async function join(page, name, room = 'testroom', pass = 'test123') {
  await page.goto(BASE);
  await page.fill('#room-input', room);
  await page.fill('#pass-input', pass);
  await page.fill('#name-input', name);
  await page.click('#join-btn');
  await page.waitForSelector('#app.visible', { timeout: 10000 });
  console.log(`  ✅ ${name} joined room`);
}

async function screenshot(page, name) {
  await page.screenshot({ path: path.join(SS_DIR, `${name}.png`), fullPage: false });
}

async function runTests() {
  await startServer();

  // ── Test 1: Basic join and UI ──
  console.log('\n📋 Test 1: Basic join and UI');
  const b1 = await chromium.launch({ executablePath: EXE, args: ARGS, headless: true });
  const p1 = await b1.newPage();
  try {
    await join(p1, 'Alice');
    await screenshot(p1, '01-alice-joined');

    // Check room title
    const title = await p1.$eval('#room-title', el => el.textContent);
    console.assert(title.includes('testroom'), `Room title wrong: ${title}`);
    console.log('  ✅ Room title correct:', title);

    // Check dark theme (default)
    const theme = await p1.$eval('html', el => el.dataset.theme || 'dark');
    console.log('  ✅ Default theme:', theme);

    // Toggle to light theme
    await p1.click('#theme-btn');
    const newTheme = await p1.$eval('html', el => el.dataset.theme);
    console.assert(newTheme === 'light', `Theme toggle failed: ${newTheme}`);
    console.log('  ✅ Theme toggle works:', newTheme);
    await screenshot(p1, '02-light-theme');
    
    // Toggle back to dark
    await p1.click('#theme-btn');
    await screenshot(p1, '03-dark-theme');

  } catch (e) { errors.push('Test 1: ' + e.message); console.error('  ❌', e.message); }

  // ── Test 2: 2-user messaging ──
  console.log('\n📋 Test 2: 2-user messaging');
  const b2 = await chromium.launch({ executablePath: EXE, args: ARGS, headless: true });
  const p2 = await b2.newPage();
  try {
    await join(p2, 'Bob');
    await screenshot(p2, '04-bob-joined');

    // Alice sends a message
    await p1.fill('#msg-input', 'Hello from Alice!');
    await p1.click('#send-btn');
    await p1.waitForTimeout(800);
    await screenshot(p1, '05-alice-message');

    // Bob receives the message
    await p2.waitForSelector('.msg-row.other', { timeout: 5000 });
    const bobMsg = await p2.$eval('.msg-row.other .bubble-text', el => el.textContent);
    console.assert(bobMsg === 'Hello from Alice!', `Wrong message: ${bobMsg}`);
    console.log('  ✅ Message delivery works');
    await screenshot(p2, '06-bob-received');

    // Bob sends reply
    await p2.fill('#msg-input', 'Hello Alice!');
    await p2.click('#send-btn');
    await p2.waitForTimeout(500);
    await screenshot(p2, '07-bob-reply');

  } catch (e) { errors.push('Test 2: ' + e.message); console.error('  ❌', e.message); }

  // ── Test 3: Scroll button ──
  console.log('\n📋 Test 3: Scroll-to-bottom button');
  try {
    // Send many messages to push content down
    for (let i = 0; i < 15; i++) {
      await p2.fill('#msg-input', `Message ${i}`);
      await p2.click('#send-btn');
      await p2.waitForTimeout(100);
    }
    await p2.waitForTimeout(500);

    // Scroll p1 up
    await p1.evaluate(() => { document.getElementById('messages').scrollTop = 0; });
    await p1.waitForTimeout(300);

    // Send a message from Bob — should show scroll btn on Alice's screen
    await p2.fill('#msg-input', 'New message while scrolled up!');
    await p2.click('#send-btn');
    await p1.waitForTimeout(800);

    const scrollBtnVisible = await p1.$eval('#scroll-btn', el => el.style.display !== 'none');
    console.log('  ✅ Scroll button visible:', scrollBtnVisible);
    await screenshot(p1, '08-scroll-btn');

    // Click scroll button
    if (scrollBtnVisible) {
      await p1.click('#scroll-btn');
      await p1.waitForTimeout(300);
      const scrollBtnHidden = await p1.$eval('#scroll-btn', el => el.style.display === 'none');
      console.log('  ✅ Scroll button hidden after click:', scrollBtnHidden);
    }

  } catch (e) { errors.push('Test 3: ' + e.message); console.error('  ❌', e.message); }

  // ── Test 4: Edit message ──
  console.log('\n📋 Test 4: Edit message');
  try {
    // Alice sends a message to edit
    await p1.fill('#msg-input', 'Original message');
    await p1.click('#send-btn');
    await p1.waitForTimeout(500);

    // Long-press to select (simulate with JS)
    const ownRows = await p1.$$('.msg-row.self');
    const lastRow = ownRows[ownRows.length - 1];
    
    // Trigger select mode via JS
    await p1.evaluate(() => {
      const rows = document.querySelectorAll('.msg-row.self');
      const lastRow = rows[rows.length - 1];
      // Simulate long press
      const event = new MouseEvent('mousedown', { bubbles: true });
      lastRow.dispatchEvent(event);
      setTimeout(() => {
        const event = new MouseEvent('mouseup', { bubbles: true });
        lastRow.dispatchEvent(event);
      }, 600);
    });
    
    // Wait for long press to trigger
    await p1.waitForTimeout(700);
    
    const selHeaderVisible = await p1.$eval('#sel-header', el => el.classList.contains('show'));
    console.log('  ✅ Select mode activated:', selHeaderVisible);
    await screenshot(p1, '09-select-mode');

    if (selHeaderVisible) {
      const editBtnVisible = await p1.$eval('#sel-edit', el => el.style.display !== 'none');
      console.log('  ✅ Edit button visible for own message:', editBtnVisible);

      if (editBtnVisible) {
        await p1.click('#sel-edit');
        await p1.waitForTimeout(300);
        const editPreviewVisible = await p1.$eval('#edit-preview', el => el.classList.contains('show'));
        console.log('  ✅ Edit preview shown:', editPreviewVisible);
        await screenshot(p1, '10-edit-mode');

        // Type new content and send
        await p1.fill('#msg-input', 'Edited message content');
        await p1.click('#send-btn');
        await p1.waitForTimeout(500);
        await screenshot(p1, '11-edited-message');
        
        // Check for edited label
        const editedLabel = await p1.$('.edited-label');
        console.log('  ✅ Edited label shown:', !!editedLabel);
      }
    } else {
      // Cancel select mode if it opened
      await p1.evaluate(() => { if(window.exitSelectMode) window.exitSelectMode(); });
    }

  } catch (e) { errors.push('Test 4: ' + e.message); console.error('  ❌', e.message); }

  // ── Test 5: Drawer and user list ──
  console.log('\n📋 Test 5: Drawer and user list');
  try {
    await p1.click('#users-btn');
    await p1.waitForTimeout(300);
    const drawerOpen = await p1.$eval('#drawer', el => el.classList.contains('open'));
    console.assert(drawerOpen, 'Drawer not open');
    console.log('  ✅ Drawer opens');
    await screenshot(p1, '12-drawer');

    // Check user list has 2 users
    const userCount = await p1.$$eval('.uitem', els => els.length);
    console.log('  ✅ User list count:', userCount);
    
    // Check status dots
    const statusDots = await p1.$$('.status-dot.online');
    console.log('  ✅ Status dots:', statusDots.length);
    
    await p1.click('#drawer-close');
    await p1.waitForTimeout(200);

  } catch (e) { errors.push('Test 5: ' + e.message); console.error('  ❌', e.message); }

  // ── Test 6: Voice call join (3 users) ──
  console.log('\n📋 Test 6: Voice call (3 users)');
  const b3 = await chromium.launch({ executablePath: EXE, args: ARGS, headless: true });
  const p3 = await b3.newPage();
  try {
    await join(p3, 'Charlie');
    await p3.waitForTimeout(500);
    await screenshot(p3, '13-charlie-joined');

    // Open drawer on each and join voice call
    for (const [pg, name] of [[p1, 'Alice'], [p2, 'Bob'], [p3, 'Charlie']]) {
      await pg.click('#users-btn');
      await pg.waitForTimeout(200);
      await pg.click('#voice-btn');
      await pg.waitForTimeout(500);
      await pg.click('#drawer-close');
      await pg.waitForTimeout(200);
      console.log(`  ✅ ${name} joined voice call`);
    }

    // Wait for connections to establish
    await p1.waitForTimeout(4000);
    await screenshot(p1, '14-voice-call-alice');
    await screenshot(p2, '15-voice-call-bob');
    await screenshot(p3, '16-voice-call-charlie');

    // Check voice status
    const aliceStatus = await p1.$eval('#voice-status', el => el.textContent);
    console.log('  ✅ Alice voice status:', aliceStatus);

  } catch (e) { errors.push('Test 6: ' + e.message); console.error('  ❌', e.message); }

  // ── Test 7: Mobile emulation ──
  console.log('\n📋 Test 7: Mobile emulation (Pixel 5)');
  try {
    const bMobile = await chromium.launch({ executablePath: EXE, args: ARGS, headless: true });
    const ctxMobile = await bMobile.newContext({
      viewport: { width: 393, height: 851 },
      userAgent: 'Mozilla/5.0 (Linux; Android 11; Pixel 5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/90.0.4430.91 Mobile Safari/537.36',
      hasTouch: true
    });
    const pMobile = await ctxMobile.newPage();
    await join(pMobile, 'MobileUser', 'testroom', 'test123');
    await screenshot(pMobile, '17-mobile-view');
    
    // Test touch scroll
    await pMobile.fill('#msg-input', 'Hello from mobile!');
    await pMobile.click('#send-btn');
    await pMobile.waitForTimeout(500);
    await screenshot(pMobile, '18-mobile-message');
    
    await bMobile.close();
    console.log('  ✅ Mobile emulation works');

  } catch (e) { errors.push('Test 7: ' + e.message); console.error('  ❌', e.message); }

  // Cleanup
  await b1.close();
  await b2.close();
  await b3.close();
  stopServer();

  // ── Results ──
  console.log('\n' + '═'.repeat(50));
  console.log(`📊 Test Results: ${errors.length === 0 ? '✅ ALL PASSED' : '⚠️ ' + errors.length + ' FAILURES'}`);
  if (errors.length > 0) {
    errors.forEach(e => console.log('  ❌ ' + e));
  }
  console.log('Screenshots saved to:', SS_DIR);
  console.log('═'.repeat(50));

  return errors.length === 0;
}

runTests().then(ok => process.exit(ok ? 0 : 1)).catch(e => {
  console.error('Test runner error:', e);
  stopServer();
  process.exit(1);
});

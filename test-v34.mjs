import { chromium } from 'playwright';
const b = await chromium.launch({executablePath:'/usr/bin/chromium-browser',args:['--no-sandbox','--use-fake-ui-for-media-stream','--use-fake-device-for-media-stream'],headless:true});
const p = await b.newPage();
const errs = [];
p.on('console', m => {
  if(m.text().includes('[md]')) console.log('MD:', m.text());
  if(m.type()==='error' && !m.text().includes('favicon')) errs.push(m.text());
});
await p.goto('http://localhost:18812/');
await p.waitForLoadState('networkidle');

const markedOk = await p.evaluate(() => typeof marked !== 'undefined');
console.log('marked:', markedOk);
const hljsOk = await p.evaluate(() => typeof hljs !== 'undefined');
console.log('hljs:', hljsOk);

await p.fill('#name-input', 'Tester');
await p.fill('#room-input', 'testroom');
await p.fill('#pass-input', 'test');
await p.click('#join-btn');
await p.waitForSelector('#app.visible', {timeout: 8000});

// Test markdown
await p.fill('#msg-input', '**bold** and `code` test');
await p.click('#send-btn');
await p.waitForTimeout(500);
const hasBold = await p.$('.bubble strong');
const hasCode = await p.$('.bubble code');
console.log('Markdown bold:', !!hasBold);
console.log('Markdown code:', !!hasCode);

// Screenshot dark mode
await p.screenshot({path: 'test-screenshots/v3.4-dark.png'});

// Switch to light mode via settings
await p.click('#settings-btn');
await p.waitForTimeout(300);
await p.screenshot({path: 'test-screenshots/v3.4-settings.png'});
await b.close();
console.log('JS errors:', errs.length);
errs.forEach(e => console.log(' -', e));
console.log('TESTS DONE');

import { chromium } from 'playwright';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

function resolveChromium() {
  const cache = path.join(os.homedir(), '.cache', 'ms-playwright');
  const versions = fs.readdirSync(cache).filter((n) => n.startsWith('chromium-')).sort().reverse();
  for (const v of versions) {
    for (const rel of ['chrome-linux64/chrome', 'chrome-linux/chrome']) {
      const c = path.join(cache, v, rel);
      if (fs.existsSync(c)) return c;
    }
  }
}

const browser = await chromium.launch({ executablePath: resolveChromium(), headless: true });
const page = await browser.newPage({ locale: 'de-DE' });
await page.goto(process.argv[2], { waitUntil: 'domcontentloaded', timeout: 60000 });
await page.waitForTimeout(2000);
const info = await page.evaluate(() => {
  const t = (s) => document.querySelector(s)?.innerText?.replace(/\s+/g, ' ').trim() || '';
  const bullets = Array.from(document.querySelectorAll('#feature-bullets li')).map((li) => li.innerText.replace(/\s+/g, ' ').trim());
  const details = Array.from(document.querySelectorAll('#productDetails_techSpec_section_1 tr, #detailBullets_feature_div li')).map((r) => r.innerText.replace(/\s+/g, ' ').trim());
  return { title: t('#title'), byline: t('#bylineInfo'), price: t('.a-price .a-offscreen'), bullets, details };
});
console.log(JSON.stringify(info, null, 1).slice(0, 6000));
await browser.close();

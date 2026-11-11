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
await page.goto('https://www.amazon.de/s?k=' + encodeURIComponent(process.argv[2] || 'tesla model y hepa aktivkohle 4 stück luftfilter'), { waitUntil: 'domcontentloaded', timeout: 60000 });
await page.waitForTimeout(2500);
const items = await page.evaluate(() => {
  return Array.from(document.querySelectorAll('div[data-asin]'))
    .filter((el) => el.getAttribute('data-asin'))
    .slice(0, 40)
    .map((el) => {
      const t = el.querySelector('h2')?.innerText || '';
      const p = el.querySelector('.a-price .a-offscreen')?.textContent || '';
      return { asin: el.getAttribute('data-asin'), title: t.replace(/\s+/g, ' ').slice(0, 110), price: p };
    });
});
console.log(JSON.stringify(items, null, 1));
await browser.close();

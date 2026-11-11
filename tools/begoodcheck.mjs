import { chromium } from 'playwright';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
function resolveChromium() {
  const cache = path.join(os.homedir(), '.cache', 'ms-playwright');
  const versions = fs.readdirSync(cache).filter((n) => n.startsWith('chromium-')).sort().reverse();
  for (const v of versions) for (const rel of ['chrome-linux64/chrome', 'chrome-linux/chrome']) {
    const c = path.join(cache, v, rel);
    if (fs.existsSync(c)) return c;
  }
}
const browser = await chromium.launch({ executablePath: resolveChromium(), headless: true });
const page = await browser.newPage();
const res = await page.goto('https://www.begood.no/products/hepa-filtere-luftinntak-tesla-model-y-4-stk.js');
const txt = await res.text();
const d = JSON.parse(txt);
console.log(d.title);
console.log('price', d.price, 'compare', d.compare_at_price, 'available', d.available);
console.log([...d.variants].map((v) => [v.title, v.price, v.compare_at_price, v.available, v.inventory_quantity]));
await browser.close();

#!/usr/bin/env node
import { chromium } from 'playwright';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const url = process.argv[2];
const postalCode = process.argv[3] || '6773';

if (!url) {
  console.error('usage: amazon-no.mjs <amazon-url> [postal-code]');
  process.exit(2);
}

function resolveChromium() {
  if (process.env.PLAYWRIGHT_CHROMIUM_PATH) {
    return process.env.PLAYWRIGHT_CHROMIUM_PATH;
  }
  const cache = path.join(os.homedir(), '.cache', 'ms-playwright');
  if (!fs.existsSync(cache)) {
    return undefined;
  }
  const versions = fs
    .readdirSync(cache)
    .filter((name) => name.startsWith('chromium-'))
    .sort()
    .reverse();
  for (const version of versions) {
    for (const relative of ['chrome-linux64/chrome', 'chrome-linux/chrome']) {
      const candidate = path.join(cache, version, relative);
      if (fs.existsSync(candidate)) {
        return candidate;
      }
    }
  }
  return undefined;
}

function parseNOK(text) {
  if (!text) {
    return null;
  }
  const cleaned = text
    .replace(/\u00a0/g, ' ')
    .replace(/NOK/gi, '')
    .replace(/[^\d.,]/g, '')
    .trim();
  if (!cleaned) {
    return null;
  }
  const lastComma = cleaned.lastIndexOf(',');
  const lastDot = cleaned.lastIndexOf('.');
  let normalized = cleaned;
  if (lastComma >= 0 && lastDot >= 0) {
    if (lastComma > lastDot) {
      normalized = cleaned.replace(/\./g, '').replace(',', '.');
    } else {
      normalized = cleaned.replace(/,/g, '');
    }
  } else if (lastComma >= 0) {
    normalized = cleaned.replace(',', '.');
  }
  const value = Number.parseFloat(normalized);
  return Number.isFinite(value) ? value : null;
}

async function bodyText(page) {
  return page.locator('body').innerText();
}

async function ensureNorway(page) {
  let text = await bodyText(page);
  if (/Deliver to\s+Norway/i.test(text)) {
    return;
  }
  const location = page.locator('#nav-global-location-popover-link');
  if (!(await location.count())) {
    throw new Error('delivery country selector not found');
  }
  await location.first().click();
  await page.waitForTimeout(1500);
  await page.selectOption('#GLUXCountryList', 'NO');
  await page.waitForTimeout(800);
  await page.evaluate(() => {
    const confirm = document.querySelector('#GLUXConfirmClose');
    confirm?.closest('.a-button')?.click();
  });
  await page.waitForTimeout(4000);
  text = await bodyText(page);
  if (!/Deliver to\s+Norway/i.test(text)) {
    throw new Error('could not set delivery country to Norway');
  }
}

async function readProduct(page) {
  const text = await bodyText(page);
  const priceText = await page
    .locator('#corePriceDisplay_desktop_feature_div .a-price .a-offscreen, #corePrice_feature_div .a-price .a-offscreen')
    .first()
    .innerText()
    .catch(() => '');
  const price = parseNOK(priceText) ?? parseNOK((text.match(/NOK\s*[\d\s.,]+/) || [''])[0]);
  if (!price) {
    throw new Error('product price not found');
  }

  const ratingText = await page
    .locator('#acrPopover')
    .first()
    .getAttribute('title')
    .catch(() => '');
  const ratingMatch = (ratingText || text).match(/([0-5][.,]\d)\s+out of 5/i);
  const rating = ratingMatch ? Number.parseFloat(ratingMatch[1].replace(',', '.')) : null;

  const countText = await page
    .locator('#acrCustomerReviewText')
    .first()
    .innerText()
    .catch(() => '');
  const countMatch = (text || countText).match(/([\d.,]+)\s+(?:global\s+)?ratings/i);
  const ratingCount = countMatch ? Number.parseInt(countMatch[1].replace(/\D/g, ''), 10) : null;

  const sellerText = await page
    .locator('#sellerProfileTriggerId, #tabular-buybox')
    .first()
    .innerText()
    .catch(() => '');
  const seller = (sellerText || '').replace(/\s+/g, ' ').trim() || null;

  const stockText = await page
    .locator('#availability')
    .first()
    .innerText()
    .catch(() => '');
  const stockLine = (stockText || text)
    .split('\n')
    .map((line) => line.split('{')[0].trim())
    .find((line) => /in stock|out of stock|available/i.test(line));
  const stock = /out of stock/i.test(stockLine || '') ? 'out_of_stock' : 'in_stock';
  const stockDetail = stockLine || null;

  return { price, rating, ratingCount, seller, stock, stockDetail };
}

async function readCart(page) {
  const add = page.locator('#add-to-cart-button');
  if (!(await add.count())) {
    throw new Error('add-to-cart button not found');
  }
  await add.click();
  await page.waitForTimeout(4000);

  await page.goto('https://www.amazon.de/gp/cart/view.html?language=en_GB', {
    waitUntil: 'domcontentloaded',
    timeout: 60000,
  });
  await page.waitForTimeout(3000);

  const text = await bodyText(page);
  if (/cannot be dispatched|not available for delivery to/i.test(text)) {
    throw new Error('item cannot be shipped to Norway');
  }

  const subtotalMatch = text.match(/Subtotal\s*\(1 item\):\s*NOK\s*([\d\s.,]+)/i);
  const subtotal = subtotalMatch ? parseNOK(subtotalMatch[1]) : null;
  if (!subtotal) {
    throw new Error('cart subtotal not found');
  }

  const importMatch = text.match(/Import Fees Deposit[^\n]*?NOK\s*([\d\s.,]+)/i);
  const importFee = importMatch ? parseNOK(importMatch[1]) : 0;
  const freeDelivery = /FREE delivery to Norway/i.test(text);
  const shipping = freeDelivery ? 0 : null;

  return {
    price: subtotal,
    shipping,
    importFee,
    delivery: freeDelivery ? 'FREE delivery to Norway' : null,
  };
}

let browser;
try {
  browser = await chromium.launch({
    executablePath: resolveChromium(),
    headless: true,
    args: ['--no-sandbox'],
  });
  const context = await browser.newContext({
    locale: 'en-GB',
    viewport: { width: 1366, height: 900 },
  });
  const page = await context.newPage();
  page.setDefaultTimeout(30000);

  await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 60000 });
  await page.waitForTimeout(2500);
  await ensureNorway(page);

  const product = await readProduct(page);
  const cart = await readCart(page);

  const shipping = cart.shipping === null ? null : cart.shipping + cart.importFee;
  const total = shipping === null ? null : cart.price + shipping;

  console.log(
    JSON.stringify({
      url,
      postal_code: postalCode,
      price_nok: cart.price,
      shipping_nok: shipping,
      total_nok: total,
      stock: product.stock,
      stock_detail: product.stockDetail,
      seller: product.seller,
      rating: product.rating,
      rating_count: product.ratingCount,
      delivery: cart.delivery,
    }),
  );
} catch (error) {
  console.log(JSON.stringify({ url, error: error instanceof Error ? error.message : String(error) }));
  process.exitCode = 1;
} finally {
  await browser?.close();
}

import assert from 'node:assert/strict';

export async function checkTimezones(browser, origin, password, javaScriptEnabled) {
 const contexts = [];
 const problems = [];
 async function open(zone) {
  const context = await browser.newContext({ javaScriptEnabled, timezoneId: zone, viewport: { width: 390, height: 844 } });
  contexts.push(context);
  const page = await context.newPage();
  page.on('pageerror', error => problems.push(error.message));
  await page.goto(origin + '/login');
  await page.getByLabel('Username', { exact: true }).fill('alex');
  await page.getByLabel('Password', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await page.waitForURL(origin + '/');
  return page;
 }
 try {
  const page = await open('America/Los_Angeles');
  await page.goto(origin + '/account');
  assert.equal(await page.getByLabel('Display timezone', { exact: true }).inputValue(), 'UTC');
  const device = page.getByRole('button', { name: "Use this device's timezone", exact: true });
  if (javaScriptEnabled) {
   await device.click();
   assert.equal(await page.getByLabel('Display timezone', { exact: true }).inputValue(), 'America/Los_Angeles');
   await page.reload();
   assert.equal(await page.getByLabel('Display timezone', { exact: true }).inputValue(), 'UTC', 'device button does not save');
  } else { assert.equal(await device.isVisible(), false); }
  await page.getByLabel('Display timezone', { exact: true }).fill('Asia/Kolkata');
  await page.getByRole('button', { name: 'Save timezone', exact: true }).click();
  await page.waitForURL(origin + '/account?saved=timezone');
  const other = await open('Pacific/Honolulu');
  await other.goto(origin + '/account');
  assert.equal(await other.getByLabel('Display timezone', { exact: true }).inputValue(), 'Asia/Kolkata');
  await other.goto(origin + '/recent');
  const link = other.locator('main a[href^="/topics/"]').first();
  await link.click();
  const stamp = other.locator('.post-content header time').first();
  await stamp.waitFor();
  assert.match(await stamp.innerText(), /IST$/);
  const instant = await stamp.getAttribute('datetime');
  assert.match(instant, /Z$/);
  await page.goto(other.url());
  assert.equal(await page.locator('.post-content header time').first().innerText(), await stamp.innerText());
  await page.goto(origin + '/account');
  await page.getByLabel('Display timezone', { exact: true }).fill('../UTC');
  await page.getByRole('button', { name: 'Save timezone', exact: true }).click();
  await page.getByRole('alert').waitFor();
  assert.match(await page.getByRole('alert').innerText(), /Choose a timezone/);
  await page.goto(origin + '/account');
  assert.equal(await page.getByLabel('Display timezone', { exact: true }).inputValue(), 'Asia/Kolkata');
  await page.getByLabel('Display timezone', { exact: true }).fill('UTC');
  await page.getByRole('button', { name: 'Save timezone', exact: true }).click();
  await page.waitForURL(origin + '/account?saved=timezone');
  assert.deepEqual(problems, []);
  console.log(`PASS: ${javaScriptEnabled ? 'HTMX' : 'JavaScript disabled'} saved display timezone across devices`);
 } finally { for (const context of contexts) await context.close(); }
}

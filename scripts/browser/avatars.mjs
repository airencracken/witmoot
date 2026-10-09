import assert from 'node:assert/strict';

const animation = Buffer.from('R0lGODlhIAAgAIEAAFJ1RQAAAAAAAAAAACH/C05FVFNDQVBFMi4wAwEAAAAh+QQACgAAACwAAAAAIAAgAAAINQABCBxIsKDBgwgTKlzIsKHDhxAjSpxIsaLFixgzatzIsaPHjyBDihxJsqTJkyhTqlzJUmRAACH5BAEKAAEALAAAAAAgACAAgat1RgAAAAAAAAAAAAg1AAEIHEiwoMGDCBMqXMiwocOHECNKnEixosWLGDNq3Mixo8ePIEOKHEmypMmTKFOqXMlSZEAAIfkEAQoAAQAsAAAAACAAIACBVXyYAAAAAAAAAAAACDUAAQgcSLCgwYMIEypcyLChw4cQI0qcSLGixYsYM2rcyLGjx48gQ4ocSbKkyZMoU6pcyVJkQAA7', 'base64');

export async function checkAvatars(browser, origin, password, javaScriptEnabled) {
 const context = await browser.newContext({ javaScriptEnabled, viewport: { width: 390, height: 844 }, reducedMotion: 'no-preference' });
 const page = await context.newPage();
 const problems = [];
 page.on('pageerror', e => problems.push(e.message));
 try {
  await page.goto(origin + '/login');
  await page.getByLabel('Username', {exact:true}).fill('alex');
  await page.getByLabel('Password', {exact:true}).fill(password);
  await page.getByRole('button', {name:'Sign in',exact:true}).click();
  await page.waitForURL(origin + '/');
  await page.getByRole('link', {name:'Your account',exact:true}).click();
  const preference = page.getByRole('checkbox', {name:'Show animated avatars',exact:true});
  if (!javaScriptEnabled) assert.equal(await preference.isChecked(), false, 'Preference survives restart');
  await preference.check();
  await page.getByRole('button', {name:'Save animation preference',exact:true}).click();
  await page.getByRole('status').filter({hasText:'animation preference has been saved'}).waitFor();
  await page.getByLabel('Choose a picture', {exact:true}).setInputFiles({name:'profile.gif',mimeType:'image/gif',buffer:animation});
  await page.getByRole('button', {name:'Save avatar',exact:true}).click();
  await page.getByRole('status').filter({hasText:'avatar has been saved'}).waitFor();
  let img = page.getByRole('img', {name:'Your current avatar'});
  await img.waitFor();
  const avatarURL = await img.getAttribute('src');
  assert.equal((await context.request.get(origin + avatarURL)).headers()['content-type'], 'image/gif');
  await page.emulateMedia({reducedMotion:'reduce'});
  await page.reload();
  await page.waitForFunction(() => document.querySelector('img[alt="Your current avatar"]').currentSrc.endsWith('?still=1'));
  assert.equal((await context.request.get(origin + avatarURL + '?still=1')).headers()['content-type'], 'image/png');
  await page.emulateMedia({reducedMotion:'no-preference'});
  await page.reload();
  await preference.uncheck();
  await page.getByRole('button', {name:'Save animation preference',exact:true}).click();
  await page.getByRole('status').filter({hasText:'animation preference has been saved'}).waitFor();
  assert.equal((await context.request.get(origin + avatarURL)).headers()['content-type'], 'image/png');
  await page.getByLabel('Choose a picture', {exact:true}).setInputFiles({name:'bad.gif',mimeType:'image/gif',buffer:Buffer.from('not a picture')});
  await page.getByRole('button', {name:'Save avatar',exact:true}).click();
  await page.getByRole('alert').waitFor();
  assert.equal((await context.request.get(origin + avatarURL)).headers()['content-type'], 'image/png');
  for (const colorScheme of ['light','dark']) {
   await page.emulateMedia({colorScheme});
   for (const width of [320,390,1280]) {
    await page.setViewportSize({width,height:844});
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),true,'Avatar settings overflow');
   }
  }
  await page.getByRole('button', {name:'Remove my avatar',exact:true}).click();
  await page.getByRole('status').filter({hasText:'avatar has been saved'}).waitFor();
  assert.equal(await page.getByRole('img', {name:'Your current avatar'}).count(),0);
  assert.equal(await preference.isChecked(),false,'Removing avatar preserves preference');
  assert.equal((await context.request.get(origin + avatarURL)).status(),404);
  assert.deepEqual(problems,[]);
  console.log(`PASS: ${javaScriptEnabled ? 'HTMX' : 'JavaScript disabled'} animated avatars, reduced motion, saved preferences, validation, removal, and responsive settings`);
 } finally { await context.close(); }
}

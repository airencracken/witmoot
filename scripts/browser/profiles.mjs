// SPDX-License-Identifier: AGPL-3.0-or-later
import assert from 'node:assert/strict';

export async function checkProfiles(browser, origin, password, javaScriptEnabled) {
 const context=await browser.newContext({javaScriptEnabled,viewport:{width:390,height:844}});
 const page=await context.newPage();
 const problems=[];
 page.on('pageerror',e=>problems.push(e.message));
 try {
  await page.goto(origin+'/login');
  await page.getByLabel('Username',{exact:true}).fill('alex');
  await page.getByLabel('Password',{exact:true}).fill(password);
  await page.getByRole('button',{name:'Sign in',exact:true}).click();
  await page.waitForURL(origin+'/');
  await page.getByRole('link',{name:'Your account',exact:true}).click();
  await page.getByRole('link',{name:'Edit your profile',exact:true}).click();
  await page.waitForURL(origin+'/account/profile');
  await page.getByLabel('Name (optional)',{exact:true}).fill('A demo name');
  await page.getByLabel('Bio (optional)',{exact:true}).fill('A short bio.\nBooks and rainy walks.');
  await page.locator('input[name="profile_link_label"]').first().fill('My site');
  await page.locator('input[name="profile_link_url"]').first().fill('https://example.org/');
  await page.getByRole('button',{name:'Save profile',exact:true}).click();
  await page.getByRole('status').filter({hasText:'Your profile has been saved.'}).waitFor();
  assert.equal(await page.getByLabel('Name (optional)',{exact:true}).inputValue(),'A demo name');
  const profile=await page.getByRole('link',{name:'View your profile',exact:true}).getAttribute('href');
  await page.getByRole('link',{name:'View your profile',exact:true}).click();
  await page.waitForURL(origin+profile);
  assert.equal(await page.getByRole('heading',{name:'alex',exact:true}).count(),1);
  assert.ok((await page.locator('main').innerText()).includes('Books and rainy walks.'));
  assert.equal(await page.getByRole('link',{name:'My site',exact:true}).getAttribute('target'),'_blank');
  assert.ok((await page.getByRole('link',{name:'My site',exact:true}).getAttribute('rel')).includes('noopener'));
  for (const route of [profile,'/account/profile']) {
   await page.goto(origin+route);
   for (const colorScheme of ['light','dark']) {
    await page.emulateMedia({colorScheme});
    for (const width of [320,390,1280]) {
     await page.setViewportSize({width,height:844});
     assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'Profile layout overflow');
    }
   }
  }
  await page.getByLabel('Name (optional)',{exact:true}).fill('');
  await page.getByLabel('Bio (optional)',{exact:true}).fill('');
  await page.locator('input[name="profile_link_label"]').first().fill('');
  await page.locator('input[name="profile_link_url"]').first().fill('');
  await page.getByRole('button',{name:'Save profile',exact:true}).click();
  await page.getByRole('status').waitFor();
  await page.goto(origin+profile);
  assert.ok((await page.locator('main').innerText()).includes('hasn’t added a bio'));
  const guest=await browser.newContext({javaScriptEnabled});
  try {
   const outsider=await guest.newPage();
   await outsider.goto(origin+profile);
   await outsider.waitForURL(/\/login(?:\?|$)/);
   assert.ok(!(await outsider.locator('body').innerText()).includes('Books and rainy walks.'));
  } finally { await guest.close(); }
  assert.deepEqual(problems,[]);
  console.log(`PASS: ${javaScriptEnabled?'HTMX':'JavaScript disabled'} optional profiles, safe links, clearing, members-only access and responsive settings`);
 } finally { await context.close(); }
}

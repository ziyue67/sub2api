const { chromium } = require(process.env.ISSUE594_PLAYWRIGHT)
const path = require('node:path')
;(async () => {
  const browser = await chromium.launch({ headless: true })
  try {
    const page = await browser.newPage({ viewport: { width: 1000, height: 1100 } })
    const failures = []
    page.on('pageerror', error => failures.push(error.message))
    for (const locale of ['en', 'zh']) {
      for (let attempt = 0; ; attempt++) {
        try { await page.goto(`http://127.0.0.1:4179/.issue594-ui.html?locale=${locale}`); break }
        catch (error) { if (attempt >= 30) throw error; await new Promise(resolve => setTimeout(resolve, 500)) }
      }
      await page.getByTestId('auth-connections').waitFor()
      await page.getByText('Codex A', { exact: true }).waitFor()
      const buttons = page.getByTestId('select-connection')
      if (await buttons.nth(1).isEnabled()) throw new Error('Identity-only SIWC connection was selectable')
      await page.screenshot({ path: path.join(process.env.RUNNER_TEMP, `issue594-connections-${locale}.png`), fullPage: true })
      await page.setViewportSize({ width: 390, height: 844 })
      await page.screenshot({ path: path.join(process.env.RUNNER_TEMP, `issue594-connections-${locale}-mobile.png`), fullPage: true })
      if (await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)) throw new Error('Mobile UI overflows viewport')
      await page.setViewportSize({ width: 1000, height: 1100 })
    }
    if (failures.length) throw new Error(failures.join('\n'))
    console.log('Actual connection component rendered in en/zh; identity-only selection disabled; screenshots contain synthetic fixtures only.')
  } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })

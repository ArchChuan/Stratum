# Playwright 临时 Spec 模板

WSL2 headless 截图有字体缺失问题（中文乱码），**不使用截图**。改用 DOM 断言 + 网络拦截。

## 临时 spec 模板

文件名 `web/e2e/tmp-xxx.spec.ts`，完成后删除：

```ts
import { test, expect } from '@playwright/test';

const BASE = 'http://localhost:3004'; // 以 Vite 实际输出端口为准

test('登录并验证目标页面数据', async ({ page }) => {
  // 1. 拦截目标 API
  const apiPromise = page.waitForResponse(
    res => res.url().includes('/api/v1/') && res.status() === 200
  );

  // 2. 登录
  await page.goto(`${BASE}/login`);
  await page.fill('input[type="email"]', process.env.E2E_EMAIL!);
  await page.fill('input[type="password"]', process.env.E2E_PASSWORD!);
  await page.click('button[type="submit"]');
  await page.waitForURL(`${BASE}/**`);

  // 3. 导航到目标页面
  await page.goto(`${BASE}/target-path`);
  const res = await apiPromise;
  const body = await res.json();

  // 4. 断言接口数据（不截图，从响应推断 UI 状态）
  expect(body.data).toBeDefined();
  expect(res.status()).toBe(200);

  // 5. DOM 文本提取（可选，不依赖字体）
  const text = await page.evaluate(() =>
    document.querySelector('[data-testid="main-content"]')?.textContent ?? ''
  );
  expect(text.length).toBeGreaterThan(0);
});
```

运行：

```bash
cd web && E2E_EMAIL=xxx E2E_PASSWORD=xxx npx playwright test e2e/tmp-xxx.spec.ts --reporter=list
```

## 验证策略优先级

1. **纯 API 验证（最快，跳过浏览器）**：前端改动涉及数据展示逻辑时，直接用 curl + JWT 验证后端接口返回值，从接口数据推断前端应渲染的内容。详见 `references/jwt-self-mint-templates.md`。
2. **Playwright DOM 断言 + 网络拦截**：不截图，改用 `page.evaluate()` 提取文本、`page.waitForResponse()` 捕获 API 响应。
3. **Windows 浏览器直访**：WSL2 端口自动转发到 Windows 宿主机，在 Windows Chrome/Edge 打开 `http://localhost:<port>` 做交互确认。

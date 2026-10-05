# 官网美术与实现记录

## 视觉方向

云端冒险启程：沿用现有猫耳云朵 Logo 与白色云尾吉祥物，以奶白、薄荷绿、天空蓝与蓝紫色为官网配色。页面面向小游戏开发者，展示仓库实际已有能力，不包含虚构客户、运营数据或托管价格。

## 美术素材

- 原 Logo：`docs/assets/branding/zekumo-github-avatar.png`；官网副本 `web/site/assets/brand.png`。
- 角色参考：`docs/assets/branding/zekumo-mascot-reference.png`。
- 生成主视觉：`web/site/assets/cloud-adventure.png`，1536 × 1024，使用内置 image_gen 工具，按上述两张图作参考生成。首屏与伙伴介绍复用同一插画，通过 CSS 布局适配，未修改角色原始设定图。

### 最终生成提示词

Use case: illustration-story. Asset type: wide website hero illustration for Zekumo, a small game backend platform. Input image 1 is the exact mascot identity reference sheet; input image 2 is brand palette reference only. Create a premium Japanese anime game key visual, landscape 1536x1024. A single adorable fluffy white fox-cat mascot matching reference precisely: enormous blue-cyan eyes, tall blue to lavender ears, blue gradient paws, a cyan Z on chest, voluminous blue-lavender cloud tail with a luminous orbital ring, wearing the dark navy headphones from reference. Mascot sits on a floating tiny grassy island on the right half, reaching a paw toward a small glowing star, with a little futuristic translucent cube beside it. Dreamy bright sky, soft cumulus clouds, distant floating islands, tiny sparkling stars and curved orbit trails. Mint, ice blue, periwinkle, cream white palette, beautifully detailed anime painting with clean linework, gentle sunlight. Compose mascot large on RIGHT two thirds, LEFT third mostly pale airy sky for HTML text overlay. Bottom fades to pale white cloud mist. No text, no lettering except mascot chest Z, no watermark, no UI, no extra characters. Maintain original character anatomy, face, fur and cloud-tail identity.

## 实现与预览

官网使用原生 HTML / CSS / JavaScript，不引入前端构建依赖。`web/embed.go` 将素材与页面嵌入 Go 二进制；`/` 为官网，`/site/` 为官网静态资源，控制台继续使用 `/admin/`。

运行完整服务后访问 `/`。仅预览静态页面可执行 `python3 -m http.server 4173 --directory web`，访问 `http://localhost:4173/site/`（该静态预览不提供控制台 API）。

支持手机导航、TypeScript / HTTP 代码页签与键盘导航、代码复制及不可用时的选中提示、FAQ 折叠、减少动态效果偏好。SDK 示例需替换 App ID、服务地址与设备标识后使用。

## 验证

- `go test ./...` 通过；托管页面测试覆盖首页、样式、脚本、图片、控制台、SSO、OAuth 与未知路径的 404。
- Chromium 实测 320、390、768、1440 像素宽度，无横向溢出。
- 实测代码页签与方向键切换、剪贴板复制、FAQ 展开、手机导航展开与选中后关闭；浏览器无控制台错误。
- 实际浏览器截图：`docs/design/website-desktop.jpg`、`docs/design/website-mobile.jpg`。
- 未部署到生产环境；静态预览不验证依赖数据库的后台业务。

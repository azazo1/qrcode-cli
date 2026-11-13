# cimbar 页面与落盘服务

`qr cimbar` 子命令用到的本地页面与文件落盘逻辑.

## 资源来源与许可证

`web/` 下的页面, 脚本与 wasm 产物来自上游
[libcimbar](https://github.com/sz3/libcimbar) 的官方发布:

- 版本: v0.6.8
- 产物: `cimbar.wasm.tar.gz` (release 附件)
- 取用日期: 2026-11-14

上游以 **Mozilla Public License 2.0** 发布, 因此:

- 许可证原文随资源一同分发: `web/libcimbar-LICENSE.txt`.
- WebAssembly 产物属于 MPL 2.0 所说的可执行形式, 上游对应的源码可从
  <https://github.com/sz3/libcimbar/tree/v0.6.8> 取得, 构建方式见该仓库的 `WASM.md`.
- 许可条款要求保留版权与许可声明, 因此页面入口与检测脚本里都标注了来源与许可,
  发布本项目时不要把 `web/` 里的许可证文件删掉.

## 目录内容

上游原样保留的文件:

- `index.html`, `recv.html`: 发送页与接收页
- `cimbar_js.<时间戳>.js` 与 `.wasm`: cimbar 编解码器
- `main.<时间戳>.js`, `send.<时间戳>.js`, `send-worker.<时间戳>.js`: 发送页逻辑
- `recv.<时间戳>.js`, `recv-worker.<时间戳>.js`: 接收页逻辑
- `zstd.<时间戳>.js`: 解压与保存
- `sw.js`, `recv-sw.js`, `pwa*.json`: service worker 与 web app 清单

本项目新增的文件:

- `landing.html`: 入口页, 用统一样式列出使用说明与发送 / 接收两个入口
- `bridge.js`: 接管接收页的保存动作, 把解码好的文件 POST 给本地服务
- `libcimbar-LICENSE.txt`: 上游许可证原文

`recv.html` 与 `index.html` 各有几处改动:

- `recv.html` 末尾追加 `<script src="bridge.js"></script>`; 保存文件时用相对路径
  `upload`, 因此页面挂在 `/` 或 `/cimbar/` 下都能工作.
- 两个页面都在自己的 `<style>` 之后引用 `assets/style.css`, 并给 `<body>` 加
  `page-cimbar-send` / `page-cimbar-recv` 类, 由共享样式接管背景, 文字与控件基调;
  页面原有的画布布局保持不变.
- 两个页面的 `<body>` 后紧跟 `<!--RETURN-->`, 服务端把它换成悬浮的返回链接.
- 两个页面都去掉了 service worker 注册: 页面由本地服务提供, 不需要离线缓存,
  而固定的预缓存清单会和随二进制一起升级的资源版本冲突, 清单里引用的图标也没有
  随上游发布包提供.

上游的其它内容没有改动.

## 升级步骤

1. 下载新版本的 `cimbar.wasm.tar.gz` 并解包, 记下其中的时间戳。
2. 用新文件替换 `web/` 下除 `landing.html`, `bridge.js`, `libcimbar-LICENSE.txt`
   以外的全部内容。
3. 恢复上面列出的注入: `bridge.js` 引用, `assets/style.css` 引用, `<body>` 上的
   页面类, `<!--RETURN-->` 占位, 以及去掉 service worker 注册.
4. 同步本文件里的版本号与源码地址。

带时间戳的文件名是上游防止缓存用的, 替换时不要改名, 页面里的引用会随之一起更新.

// 本文件由 qr cimbar 提供, 不属于上游 libcimbar.
//
// 上游接收页在文件重组完成后调用 Zstd.download_blob 触发浏览器下载, 这里把它
// 换成 POST /upload, 由本地 Go 服务保存到用户指定的目录, 页面因此不弹出下载条,
// 也不依赖浏览器的下载目录.
//
// 保存失败时退回原本的浏览器下载, 保证文件不会因为服务端出错而丢失.

(function () {
  if (typeof Zstd === "undefined" || typeof Zstd.download_blob !== "function") {
    return;
  }

  const originalDownload = Zstd.download_blob.bind(Zstd);

  function formatSize(bytes) {
    if (!bytes) {
      return "0 B";
    }
    const units = ["B", "KB", "MB", "GB"];
    let value = bytes;
    let i = 0;
    while (value >= 1024 && i < units.length - 1) {
      value /= 1024;
      i++;
    }
    return (i === 0 ? value : value.toFixed(1)) + " " + units[i];
  }

  function showToast(text, isError) {
    let el = document.getElementById("__cimbar_save_toast");
    if (!el) {
      el = document.createElement("div");
      el.id = "__cimbar_save_toast";
      el.style.cssText = [
        "position:fixed",
        "left:50%",
        "bottom:1.5rem",
        "transform:translateX(-50%)",
        "z-index:9999",
        "max-width:min(90vw,32rem)",
        "padding:0.75rem 1rem",
        "border-radius:8px",
        "font:0.9rem/1.5 system-ui,sans-serif",
        "color:#ffffff",
        "box-shadow:0 6px 24px rgba(0,0,0,0.3)",
        "white-space:pre-wrap",
        "word-break:break-all",
        "box-sizing:border-box",
      ].join(";");
      document.body.appendChild(el);
    }
    el.style.background = isError ? "#b91c1c" : "#111827";
    el.textContent = text;
    el.style.display = "block";
    clearTimeout(el.__hideTimer);
    if (!isError) {
      el.__hideTimer = setTimeout(function () {
        el.style.display = "none";
      }, 8000);
    }
  }

  async function save(name, blob) {
    showToast("正在保存 " + name + " (" + formatSize(blob.size) + ") ...", false);
    try {
      const resp = await fetch("/upload", {
        method: "POST",
        headers: { "X-Filename": encodeURIComponent(name) },
        body: blob,
      });
      const data = await resp.json().catch(function () {
        return {};
      });
      if (!resp.ok) {
        throw new Error(data.error || "HTTP " + resp.status);
      }
      showToast("已保存到 " + data.path, false);
    } catch (err) {
      showToast("保存到本地目录失败: " + err.message + "\n已改为浏览器下载.", true);
      originalDownload(name, blob);
    }
  }

  Zstd.download_blob = function (name, blob) {
    save(name, blob);
  };
})();

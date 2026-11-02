// Copy-to-clipboard buttons on code blocks. Same file is inlined into docs/index.html.
// Without JavaScript no button is shown. For a `console` block only the commands are copied
// (lines starting with "$ ", prompt removed).
(function () {
  "use strict";
  function text(pre) {
    var t = (pre.querySelector("code") || pre).textContent.replace(/\n$/, "");
    if (pre.getAttribute("data-lang") === "console") {
      var cmds = t.split("\n").filter(function (l) { return /^\$ /.test(l); });
      if (cmds.length) t = cmds.map(function (l) { return l.slice(2); }).join("\n");
    }
    return t;
  }
  function fallback(t) {
    var ta = document.createElement("textarea");
    ta.value = t;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    var ok = false;
    try { ok = document.execCommand("copy"); } catch (e) { ok = false; }
    document.body.removeChild(ta);
    return ok ? Promise.resolve() : Promise.reject();
  }
  function copy(t) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(t).catch(function () { return fallback(t); });
    }
    return fallback(t);
  }
  Array.prototype.forEach.call(document.querySelectorAll("pre"), function (pre) {
    var wrap = pre.parentNode;
    if (!wrap.classList.contains("code")) {
      wrap = document.createElement("div");
      wrap.className = "code";
      pre.parentNode.insertBefore(wrap, pre);
      wrap.appendChild(pre);
    }
    var btn = document.createElement("button");
    btn.type = "button";
    btn.className = "copy";
    btn.textContent = "Copy";
    btn.setAttribute("aria-label", "Copy code to clipboard");
    var timer;
    btn.addEventListener("click", function () {
      copy(text(pre)).then(function () {
        btn.textContent = "Copied";
        btn.classList.add("done");
      }, function () {
        btn.textContent = "Copy failed";
      }).then(function () {
        clearTimeout(timer);
        timer = setTimeout(function () {
          btn.textContent = "Copy";
          btn.classList.remove("done");
        }, 1600);
      });
    });
    wrap.appendChild(btn);
  });
})();

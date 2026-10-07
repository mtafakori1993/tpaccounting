// Applies the saved theme and language before first paint to avoid a flash.
(function () {
  try {
    var t = localStorage.getItem('tp.theme');
    if (!t) t = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    document.documentElement.dataset.theme = t;
    var l = localStorage.getItem('tp.lang') || 'fa';
    document.documentElement.lang = l;
    document.documentElement.dir = l === 'fa' ? 'rtl' : 'ltr';
  } catch (e) {}
})();

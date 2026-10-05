(() => {
  'use strict';
  const menu = document.querySelector('.menu-toggle');
  const navigation = document.querySelector('#navigation');
  const closeMenu = () => {
    menu.setAttribute('aria-expanded', 'false');
    menu.setAttribute('aria-label', '打开导航');
    navigation.classList.remove('open');
  };
  menu.addEventListener('click', () => {
    const open = menu.getAttribute('aria-expanded') !== 'true';
    menu.setAttribute('aria-expanded', String(open));
    menu.setAttribute('aria-label', open ? '关闭导航' : '打开导航');
    navigation.classList.toggle('open', open);
  });
  navigation.addEventListener('click', (event) => {
    if (event.target.closest('a')) closeMenu();
  });
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && menu.getAttribute('aria-expanded') === 'true') {
      closeMenu();
      menu.focus();
    }
  });
  document.addEventListener('click', (event) => {
    if (!event.target.closest('.header')) closeMenu();
  });
  window.matchMedia('(min-width: 761px)').addEventListener('change', closeMenu);

  const tabs = [...document.querySelectorAll('[role="tab"]')];
  const activateTab = (tab) => {
    tabs.forEach((item) => {
      const selected = item === tab;
      item.setAttribute('aria-selected', String(selected));
      item.tabIndex = selected ? 0 : -1;
      document.getElementById(item.getAttribute('aria-controls')).hidden = !selected;
    });
    document.querySelector('.window-top > span').textContent = tab.id === 'tab-ts' ? 'hello-world.ts' : 'hello-world.sh';
  };
  tabs.forEach((tab, index) => {
    tab.addEventListener('click', () => activateTab(tab));
    tab.addEventListener('keydown', (event) => {
      let target;
      if (event.key === 'ArrowRight') target = tabs[(index + 1) % tabs.length];
      if (event.key === 'ArrowLeft') target = tabs[(index - 1 + tabs.length) % tabs.length];
      if (event.key === 'Home') target = tabs[0];
      if (event.key === 'End') target = tabs[tabs.length - 1];
      if (target) {
        event.preventDefault();
        activateTab(target);
        target.focus();
      }
    });
  });

  let toastTimer;
  const notify = (message) => {
    const toast = document.querySelector('#announcement');
    toast.textContent = message;
    toast.classList.add('visible');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toast.classList.remove('visible'), 3200);
  };
  document.querySelector('#copy-code').addEventListener('click', async () => {
    const panel = document.querySelector('[role="tabpanel"]:not([hidden])');
    const code = panel.querySelector('code').textContent;
    try {
      await navigator.clipboard.writeText(code);
      notify('代码已复制，开始你的创作吧 ✧');
    } catch {
      const selection = window.getSelection();
      const range = document.createRange();
      range.selectNodeContents(panel.querySelector('code'));
      selection.removeAllRanges();
      selection.addRange(range);
      notify('代码已选中，请按 Ctrl+C 或 ⌘C 复制');
    }
  });
})();

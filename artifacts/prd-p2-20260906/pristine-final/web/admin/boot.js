// Starts the app and adds the two Material behaviours that need script:
// press ripples and the light/dark toggle.

// --- theme: follow the system until the user picks a side ---
const THEME_KEY = 'mc_admin_theme';
const applyTheme = mode => mode === 'system'
  ? document.documentElement.removeAttribute('data-theme')
  : document.documentElement.setAttribute('data-theme', mode);
const currentTheme = () => localStorage.getItem(THEME_KEY) || 'system';
applyTheme(currentTheme());

const THEME_LABEL = { system: '跟随系统', light: '浅色', dark: '深色' };
function themeLabel() { return THEME_LABEL[currentTheme()]; }

function cycleTheme() {
  const next = { system: 'light', light: 'dark', dark: 'system' }[currentTheme()];
  localStorage.setItem(THEME_KEY, next);
  applyTheme(next);
  const label = document.getElementById('menuTheme');
  if (label) label.textContent = themeLabel();
  toast('主题:' + THEME_LABEL[next]);
}
document.getElementById('themeBtn').onclick = cycleTheme;

// --- ripple: expands from the press point on button-like surfaces ---
document.addEventListener('pointerdown', e => {
  const target = e.target.closest('.btn, .icon-btn, .chip, .nav-item, .list-item');
  if (!target) return;
  const rect = target.getBoundingClientRect();
  const size = Math.max(rect.width, rect.height);
  const ripple = document.createElement('span');
  ripple.className = 'ripple';
  ripple.style.width = ripple.style.height = size + 'px';
  ripple.style.left = e.clientX - rect.left - size / 2 + 'px';
  ripple.style.top = e.clientY - rect.top - size / 2 + 'px';
  target.appendChild(ripple);
  ripple.addEventListener('animationend', () => ripple.remove());
});

document.getElementById('loginPass').addEventListener('keydown', e => {
  if (e.key === 'Enter') login();
});
document.addEventListener('keydown', e => { if (e.key === 'Escape') closeDialog(); });

(async () => {
  if (!token) {
    document.getElementById('loginView').classList.remove('hidden');
    return;
  }
  try {
    await enterApp();
  } catch {
    logout();
    document.getElementById('loginView').classList.remove('hidden');
  }
})();

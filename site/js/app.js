(() => {
  // Replace this once the repo is public. Keeping it in one place avoids hunting through markup.
  const REPO_URL = 'https://github.com/z19r/smbark';

  document.querySelectorAll('[data-repo-link]').forEach((link) => {
    link.href = REPO_URL;
    if (REPO_URL.startsWith('http')) {
      link.target = '_blank';
      link.rel = 'noreferrer';
    }
  });

  const carousel = document.querySelector('[data-carousel]');
  if (carousel) {
    const slides = [...carousel.querySelectorAll('[data-slide]')];
    const dots = [...carousel.querySelectorAll('[data-dot]')];
    const stage = carousel.querySelector('.carousel-stage');
    let current = 0;
    let touchStartX = null;

    const show = (next) => {
      current = (next + slides.length) % slides.length;
      slides.forEach((slide, index) => slide.classList.toggle('is-active', index === current));
      dots.forEach((dot, index) => {
        const active = index === current;
        dot.classList.toggle('is-active', active);
        dot.setAttribute('aria-selected', String(active));
        dot.tabIndex = active ? 0 : -1;
      });
    };

    carousel.querySelector('[data-prev]').addEventListener('click', () => show(current - 1));
    carousel.querySelector('[data-next]').addEventListener('click', () => show(current + 1));
    dots.forEach((dot) => dot.addEventListener('click', () => show(Number(dot.dataset.dot))));

    stage.addEventListener('keydown', (event) => {
      if (event.key === 'ArrowLeft') { event.preventDefault(); show(current - 1); }
      if (event.key === 'ArrowRight') { event.preventDefault(); show(current + 1); }
    });
    stage.addEventListener('touchstart', (event) => { touchStartX = event.touches[0]?.clientX ?? null; }, { passive: true });
    stage.addEventListener('touchend', (event) => {
      if (touchStartX == null) return;
      const endX = event.changedTouches[0]?.clientX ?? touchStartX;
      const dx = endX - touchStartX;
      if (Math.abs(dx) > 45) show(current + (dx < 0 ? 1 : -1));
      touchStartX = null;
    }, { passive: true });
  }

  // Intentionally easy to swap when package names/commands are final.
  const installCommands = {
    omarchy: 'omarchy install smbark',
    arch: 'yay -S smbark',
    debian: 'sudo apt install smbark',
    source: 'go install github.com/YOUR_GITHUB/smbark@latest'
  };

  const installCode = document.querySelector('[data-install-code]');
  const installTabs = [...document.querySelectorAll('[data-install-tab]')];
  installTabs.forEach((tab) => {
    tab.addEventListener('click', () => {
      installTabs.forEach((item) => {
        const active = item === tab;
        item.classList.toggle('is-active', active);
        item.setAttribute('aria-selected', String(active));
      });
      installCode.textContent = installCommands[tab.dataset.installTab];
    });
  });

  document.querySelector('[data-copy-install]')?.addEventListener('click', async (event) => {
    const button = event.currentTarget;
    try {
      await navigator.clipboard.writeText(installCode.textContent.trim());
      const old = button.textContent;
      button.textContent = 'Copied';
      window.setTimeout(() => { button.textContent = old; }, 1100);
    } catch {
      button.textContent = 'Select + copy';
    }
  });
})();

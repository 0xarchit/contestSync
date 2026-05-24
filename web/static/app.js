let lenis;
let csrfToken = '';

function initApp() {
    console.log('ContestSync: Initializing App...');
    
    if (typeof gsap === 'undefined' || typeof ScrollTrigger === 'undefined') {
        console.error('ContestSync: GSAP or ScrollTrigger not found. UI animations disabled.');
        return;
    }

    const path = window.location.pathname;
    const isPreferencesPage = path.includes('preferences');

    if (isPreferencesPage) {
        initPreferences();
        return;
    }

    const rm = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

    if (!rm && typeof Lenis !== 'undefined') {
        console.log('ContestSync: Starting Lenis...');
        lenis = new Lenis({
            duration: 1.2,
            easing: t => Math.min(1, 1.001 - Math.pow(2, -10 * t)),
            smoothWheel: true,
            wheelMultiplier: 1,
            touchMultiplier: 2,
        });

        gsap.ticker.add(t => {
            lenis.raf(t * 1000);
        });
        gsap.ticker.lagSmoothing(0);

        lenis.on('scroll', () => {
            ScrollTrigger.update();
        });
    }

    gsap.registerPlugin(ScrollTrigger);

    const heroContent = document.querySelectorAll('.hero-content > *, .hero-visual');
    if (heroContent.length > 0) {
        gsap.set(heroContent, { visibility: 'visible' });

        let heroTl = gsap.timeline({ 
            delay: 0.2,
            onComplete: () => ScrollTrigger.refresh()
        });

        heroTl
            .from('.badge', { opacity: 0, y: 20, duration: 0.6, ease: 'power3.out' })
            .from('.hero-title .line', { opacity: 0, y: 60, stagger: 0.12, duration: 0.9, ease: 'expo.out' }, '-=0.4')
            .from('.hero-subtext', { opacity: 0, y: 20, duration: 0.6, ease: 'power3.out' }, '-=0.6')
            .from('.hero-actions', { opacity: 0, y: 20, duration: 0.6, ease: 'power3.out' }, '-=0.4')
            .from('.hero-visual', { opacity: 0, x: 40, scale: 0.95, duration: 1, ease: 'expo.out' }, '-=0.7');
    }

    if (!rm) {
        if (document.querySelector('.hero-bg')) {
            gsap.to('.hero-bg', {
                y: 100, ease: 'none',
                scrollTrigger: { trigger: '.hero', start: 'top top', end: 'bottom top', scrub: true }
            });
        }
        if (document.querySelector('.hero-visual')) {
            gsap.to('.hero-visual', {
                y: -100, ease: 'none',
                scrollTrigger: { trigger: '.hero', start: 'top top', end: 'bottom top', scrub: true }
            });
        }
    }

    let si = document.querySelector('.scroll-indicator');
    if (si) {
        ScrollTrigger.create({
            trigger: '.hero',
            start: 'top top+=50',
            onEnter: () => si.classList.add('is-hidden'),
            onLeaveBack: () => si.classList.remove('is-hidden'),
        });
    }

    if (document.querySelector('.problem-card')) {
        gsap.from('.problem-card', {
            opacity: 0, y: 50, stagger: 0.15, duration: 0.8, ease: 'power4.out',
            scrollTrigger: { trigger: '.problem', start: 'top 80%' },
        });
    }

    if (!rm && document.querySelector('.solution')) {
        let solTl = gsap.timeline({
            scrollTrigger: {
                trigger: '.solution',
                start: 'top top',
                end: '+=400%',
                pin: true,
                scrub: 1,
                anticipatePin: 1
            }
        });

        let steps = document.querySelectorAll('.step');
        let lineFgs = document.querySelectorAll('.line-fg');

        steps.forEach((step, i) => {
            solTl.to(step, { 
                opacity: 1, 
                duration: 1,
                onStart: () => step.classList.add('active'),
                onReverseComplete: () => step.classList.remove('active')
            }, i * 2.5);

            if (lineFgs[i]) {
                solTl.to(lineFgs[i], { 
                    attr: { 'stroke-dashoffset': 0 }, 
                    duration: 1.5 
                }, (i * 2.5) + 0.8);
            }
        });
    }

    if (document.querySelector('.platform-card')) {
        gsap.from('.platform-card', {
            opacity: 0, scale: 0.9, y: 30, stagger: 0.08, duration: 0.8, ease: 'back.out(1.2)',
            scrollTrigger: { trigger: '.platforms', start: 'top 85%' },
        });
    }

    if (document.querySelector('.fake-calendar')) {
        gsap.from('.fake-calendar', {
            opacity: 0, y: 60, scale: 0.95, duration: 1.2, ease: 'expo.out',
            scrollTrigger: {
                trigger: '.calendar-preview',
                start: 'top 75%',
                onEnter: () => {
                    gsap.to('.event', { opacity: 1, scaleY: 1, stagger: 0.12, duration: 0.7, ease: 'power4.out' });
                    document.querySelector('.fake-calendar')?.classList.add('shimmer');
                }
            }
        });
    }

    document.querySelectorAll('.txt-reveal').forEach(el => {
        ScrollTrigger.create({
            trigger: el,
            start: 'top 90%',
            onEnter: () => el.classList.add('filled'),
            onLeaveBack: () => el.classList.remove('filled'),
        });
    });

    document.querySelectorAll('.reveal-wrap').forEach(w => {
        let child = w.firstElementChild;
        if (child) {
            gsap.from(child, {
                y: 100, duration: 1, ease: 'power4.out',
                scrollTrigger: { trigger: w, start: 'top 95%' },
            });
        }
    });

    document.querySelectorAll('a[href^="#"]').forEach(a => {
        a.addEventListener('click', e => {
            e.preventDefault();
            let target = document.querySelector(a.getAttribute('href'));
            if (target) {
                if (lenis) lenis.scrollTo(target);
                else target.scrollIntoView({ behavior: 'smooth' });
            }
        });
    });

    fetch('/me', { credentials: 'same-origin' })
        .then(res => {
            if (res.ok) {
                document.querySelectorAll('a[href="/auth/google"]').forEach(btn => {
                    btn.href = 'preferences.html';
                    btn.innerHTML = 'Go to Preferences <svg class="icon-ext" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><line x1="5" y1="12" x2="19" y2="12"></line><polyline points="12 5 19 12 12 19"></polyline></svg>';
                });
            }
        })
        .catch(err => console.error(err));

    ScrollTrigger.refresh();
}

async function initPreferences() {
    console.log('ContestSync: Initializing Preferences...');
    let card = document.querySelector('.pref-card');
    if (card) gsap.fromTo(card, { opacity: 0, y: 20 }, { opacity: 1, y: 0, duration: 0.5, ease: 'power3.out' });

    let existingPlatforms = [];
    try {
        let meRes = await fetch('/me', { credentials: 'same-origin' });
        if (meRes.ok) {
            let me = await meRes.json();
            csrfToken = me.csrf_token || '';
            let emailEl = document.getElementById('user-email');
            if (emailEl && me.email) emailEl.textContent = me.email;
            existingPlatforms = me.platforms || [];
            let useDedicatedCheckbox = document.getElementById('use-dedicated');
            if (useDedicatedCheckbox) {
                useDedicatedCheckbox.checked = !!me.use_dedicated;
            }
        } else if (meRes.status === 401) {
            window.location.href = '/auth/google';
            return;
        }
    } catch (e) {
        console.error('Pref: me fetch failed', e);
    }

    try {
        let pRes = await fetch('/platforms', { credentials: 'same-origin' });
        let container = document.getElementById('platforms-list');
        if (!container) return;

        if (pRes.ok) {
            let data = await pRes.json();
            let platforms = data.platforms || [];
            
            let colorMap = {
                leetcode: 'var(--platform-leetcode)',
                codeforces: 'var(--platform-codeforces)',
                codechef: 'var(--platform-codechef)',
                atcoder: 'var(--platform-atcoder)',
                hackerrank: 'var(--platform-hackerrank)',
                geeksforgeeks: 'var(--platform-gfg)',
                code360: 'var(--platform-code360)',
            };

            container.innerHTML = '';
            platforms.forEach(p => {
                let label = document.createElement('label');
                label.className = 'platform-item';
                let isChecked = existingPlatforms.length === 0 || existingPlatforms.includes(p);
                label.innerHTML = `
                    <input type="checkbox" name="platform" value="${p}" ${isChecked ? 'checked' : ''}>
                    <div class="custom-checkbox"></div>
                    <div class="p-dot" style="background: ${colorMap[p] || 'var(--text-dim)'}"></div>
                    <span class="p-label">${p.charAt(0).toUpperCase() + p.slice(1)}</span>
                `;
                container.appendChild(label);
            });
        }

        let form = document.getElementById('pref-form');
        if (form) {
            form.addEventListener('submit', async e => {
                e.preventDefault();
                let submitBtn = form.querySelector('button[type="submit"]');
                submitBtn.disabled = true;
                submitBtn.textContent = 'Syncing...';

                let selected = Array.from(document.querySelectorAll('input[name="platform"]:checked')).map(i => i.value);
                let useDedicated = false;
                let useDedicatedCheckbox = document.getElementById('use-dedicated');
                if (useDedicatedCheckbox) {
                    useDedicated = useDedicatedCheckbox.checked;
                }
                try {
                    let res = await securePost('/preferences', { platforms: selected, use_dedicated: useDedicated });
                    if (res) {
                        await securePost('/sync', {});
                        showSuccess();
                    }
                } catch (err) {
                    console.error('Pref: save failed', err);
                    submitBtn.disabled = false;
                    submitBtn.textContent = 'Start Sync';
                }
            });
        }

        if (card) {
            let delBtn = document.createElement('button');
            delBtn.className = 'btn btn-ghost';
            delBtn.style.cssText = 'width:100%; justify-content:center; margin-top:1rem; color:var(--text-dim); font-size:var(--t-label);';
            delBtn.textContent = 'Remove Account & Data';
            delBtn.addEventListener('click', async () => {
                if (confirm('Are you sure? This will remove all your data and stop calendar syncing.')) {
                    try {
                        const res = await fetch('/account', {
                            method: 'DELETE',
                            headers: { 'X-CSRF-Token': getCSRFToken() },
                            credentials: 'same-origin'
                        });
                        if (res.ok) {
                            window.location.href = '/';
                        }
                    } catch (err) {
                        console.error('Delete failed', err);
                    }
                }
            });
            card.appendChild(delBtn);
        }

    } catch (e) {
        console.error('Pref: platforms fetch failed', e);
    }
}

async function securePost(url, body) {
    let res = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCSRFToken() },
        credentials: 'same-origin',
        body: JSON.stringify(body),
    });
    if (!res.ok) { 
        let e = await res.json().catch(() => ({ error: 'request failed' })); 
        throw new Error(e.error); 
    }
    return res.json();
}

function getCSRFToken() {
    return csrfToken;
}

function showSuccess() {
    let card = document.querySelector('.pref-card');
    if (!card) return;
    
    gsap.to(card, {
        opacity: 0,
        y: -20,
        duration: 0.4,
        onComplete: () => {
            card.innerHTML = `
                <div class="success-state centered">
                    <div style="margin-bottom:2rem;">
                        <svg width="64" height="64" viewBox="0 0 24 24" fill="none" stroke="var(--accent-cyan)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                            <polyline points="20 6 9 17 4 12"></polyline>
                        </svg>
                    </div>
                    <h1>You're all set.</h1>
                    <p>Contests are syncing to your Google Calendar.</p>
                    <a href="https://calendar.google.com" target="_blank" rel="noopener noreferrer" class="btn btn-primary" style="margin-top:1rem;">
                        Open Google Calendar
                    </a>
                </div>
            `;
            gsap.fromTo(card, { opacity: 0, y: 20 }, { opacity: 1, y: 0, duration: 0.5, ease: 'power3.out' });
        }
    });
}

function initGlobalInteractivity() {
    const grid = document.createElement('div');
    grid.className = 'hc-grid';
    const vignette = document.createElement('div');
    vignette.className = 'hc-vignette';
    const scanlines = document.createElement('div');
    scanlines.className = 'hc-scanlines';
    document.body.prepend(scanlines);
    document.body.prepend(vignette);
    document.body.prepend(grid);

    if (window.matchMedia('(hover: hover)').matches) {
        const cursor = document.createElement('div');
        cursor.className = 'custom-cursor';
        const cursorDot = document.createElement('div');
        cursorDot.className = 'custom-cursor-dot';
        document.body.appendChild(cursor);
        document.body.appendChild(cursorDot);

        document.addEventListener('mousemove', e => {
            gsap.to(cursor, { x: e.clientX, y: e.clientY, duration: 0.3, ease: 'power2.out' });
            gsap.to(cursorDot, { x: e.clientX, y: e.clientY, duration: 0.05 });
        });

        document.addEventListener('mousedown', () => {
            gsap.to(cursor, { scale: 0.8, duration: 0.1 });
        });

        document.addEventListener('mouseup', () => {
            gsap.to(cursor, { scale: 1, duration: 0.15 });
        });

        const addHoverListeners = () => {
            document.querySelectorAll('a, button, .btn, .platform-card, .platform-item, .custom-checkbox, input[type="checkbox"]').forEach(el => {
                if (!el.dataset.cursorBound) {
                    el.dataset.cursorBound = 'true';
                    el.addEventListener('mouseenter', () => cursor.classList.add('hover'));
                    el.addEventListener('mouseleave', () => cursor.classList.remove('hover'));
                }
            });
        };

        addHoverListeners();
        setInterval(addHoverListeners, 1000);
    }

    document.querySelectorAll('.platform-card').forEach(card => {
        card.addEventListener('mousemove', e => {
            const rect = card.getBoundingClientRect();
            const x = e.clientX - rect.left;
            const y = e.clientY - rect.top;
            card.style.setProperty('--mx', `${x}px`);
            card.style.setProperty('--my', `${y}px`);

            const tiltX = x - rect.width / 2;
            const tiltY = y - rect.height / 2;
            gsap.to(card, {
                rotateY: tiltX * 0.08,
                rotateX: -tiltY * 0.08,
                duration: 0.3,
                ease: 'power2.out'
            });
        });

        card.addEventListener('mouseleave', () => {
            gsap.to(card, {
                rotateY: 0,
                rotateX: 0,
                duration: 0.5,
                ease: 'power2.out'
            });
        });
    });
}

window.addEventListener('load', () => {
    setTimeout(() => {
        initApp();
        initGlobalInteractivity();
    }, 100);
});

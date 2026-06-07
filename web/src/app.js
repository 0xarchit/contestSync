import './style.css';

let csrfToken = "";

function initApp() {
  const path = window.location.pathname;
  const isPreferencesPage = path.includes("preferences");

  if (isPreferencesPage) {
    initPreferences();
    return;
  }

  const rm = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  const heroContent = document.querySelectorAll(
    ".hero-content > *, .hero-visual",
  );
  if (heroContent.length > 0) {
    heroContent.forEach((el, index) => {
      el.style.visibility = "visible";
      if (!rm) {
        const isVisual = el.classList.contains("hero-visual");
        el.animate(
          [
            { 
              opacity: 0, 
              transform: isVisual ? "translateX(40px) scale(0.95)" : "translateY(20px)" 
            },
            { 
              opacity: 1, 
              transform: "none" 
            }
          ],
          {
            duration: isVisual ? 1000 : 600,
            delay: 200 + index * 120,
            fill: "forwards",
            easing: "cubic-bezier(0.16, 1, 0.3, 1)"
          }
        );
      }
    });
  }

  if (!rm) {
    window.addEventListener("scroll", () => {
      const scrolled = window.scrollY;
      const heroBg = document.querySelector(".hero-bg");
      if (heroBg) {
        heroBg.style.transform = `translateY(${scrolled * 0.2}px)`;
      }
      const heroVisual = document.querySelector(".hero-visual");
      if (heroVisual) {
        heroVisual.style.transform = `translateY(-${scrolled * 0.15}px)`;
      }
    });
  }

  let si = document.querySelector(".scroll-indicator");
  if (si) {
    window.addEventListener("scroll", () => {
      if (window.scrollY > 50) {
        si.classList.add("is-hidden");
      } else {
        si.classList.remove("is-hidden");
      }
    });
  }

  const problemCards = document.querySelectorAll(".problem-card");
  if (problemCards.length > 0) {
    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            problemCards.forEach((card, index) => {
              if (rm) {
                card.style.opacity = "1";
                card.style.transform = "none";
              } else {
                card.animate(
                  [
                    { opacity: 0, transform: "translateY(50px)" },
                    { opacity: 1, transform: "none" }
                  ],
                  {
                    duration: 800,
                    delay: index * 150,
                    fill: "forwards",
                    easing: "cubic-bezier(0.25, 1, 0.5, 1)"
                  }
                );
              }
            });
            observer.disconnect();
          }
        });
      },
      { threshold: 0.1 }
    );
    const problemSec = document.querySelector(".problem");
    if (problemSec) observer.observe(problemSec);
  }

  const steps = document.querySelectorAll(".step");
  const lineFgs = document.querySelectorAll(".line-fg");
  if (steps.length > 0) {
    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            entry.target.classList.add("active");
            const idx = Array.from(steps).indexOf(entry.target);
            if (idx >= 0 && lineFgs[idx]) {
              if (rm) {
                lineFgs[idx].style.strokeDashoffset = "0";
              } else {
                lineFgs[idx].style.transition = "stroke-dashoffset 0.8s ease-out";
                lineFgs[idx].style.strokeDashoffset = "0";
              }
            }
          }
        });
      },
      { threshold: 0.5 }
    );
    steps.forEach((step) => observer.observe(step));
  }

  const platformCards = document.querySelectorAll(".platform-card");
  if (platformCards.length > 0) {
    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            platformCards.forEach((card, index) => {
              if (rm) {
                card.style.opacity = "1";
                card.style.transform = "none";
              } else {
                card.animate(
                  [
                    { opacity: 0, transform: "scale(0.9) translateY(30px)" },
                    { opacity: 1, transform: "none" }
                  ],
                  {
                    duration: 800,
                    delay: index * 80,
                    fill: "forwards",
                    easing: "cubic-bezier(0.175, 0.885, 0.32, 1.275)"
                  }
                );
              }
            });
            observer.disconnect();
          }
        });
      },
      { threshold: 0.1 }
    );
    const platformsSec = document.querySelector(".platforms");
    if (platformsSec) observer.observe(platformsSec);
  }

  const calendarPreview = document.querySelector(".calendar-preview");
  const fakeCalendar = document.querySelector(".fake-calendar");
  const events = document.querySelectorAll(".event");
  if (calendarPreview && fakeCalendar) {
    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            if (rm) {
              fakeCalendar.style.opacity = "1";
              fakeCalendar.style.transform = "none";
              events.forEach((ev) => {
                ev.style.opacity = "1";
                ev.style.transform = "none";
              });
            } else {
              fakeCalendar.animate(
                [
                  { opacity: 0, transform: "translateY(60px) scale(0.95)" },
                  { opacity: 1, transform: "none" }
                ],
                {
                  duration: 1200,
                  easing: "cubic-bezier(0.16, 1, 0.3, 1)",
                  fill: "forwards"
                }
              );
              events.forEach((ev, idx) => {
                ev.animate(
                  [
                    { opacity: 0, transform: "scaleY(0)" },
                    { opacity: 1, transform: "scaleY(1)" }
                  ],
                  {
                    duration: 700,
                    delay: idx * 120,
                    easing: "cubic-bezier(0.25, 1, 0.5, 1)",
                    fill: "forwards"
                  }
                );
              });
            }
            fakeCalendar.classList.add("shimmer");
            observer.disconnect();
          }
        });
      },
      { threshold: 0.2 }
    );
    observer.observe(calendarPreview);
  }

  const txtReveals = document.querySelectorAll(".txt-reveal");
  if (txtReveals.length > 0) {
    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            entry.target.classList.add("filled");
          } else {
            entry.target.classList.remove("filled");
          }
        });
      },
      { threshold: 0.1 }
    );
    txtReveals.forEach((el) => observer.observe(el));
  }

  const revealWraps = document.querySelectorAll(".reveal-wrap");
  if (revealWraps.length > 0) {
    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            const child = entry.target.firstElementChild;
            if (child) {
              if (rm) {
                child.style.transform = "none";
              } else {
                child.animate(
                  [
                    { transform: "translateY(100px)" },
                    { transform: "translateY(0)" }
                  ],
                  {
                    duration: 1000,
                    easing: "cubic-bezier(0.25, 1, 0.5, 1)",
                    fill: "forwards"
                  }
                );
              }
            }
            observer.unobserve(entry.target);
          }
        });
      },
      { threshold: 0.05 }
    );
    revealWraps.forEach((w) => observer.observe(w));
  }

  document.querySelectorAll('a[href^="#"]').forEach((a) => {
    a.addEventListener("click", (e) => {
      e.preventDefault();
      let target = document.querySelector(a.getAttribute("href"));
      if (target) {
        target.scrollIntoView({ behavior: "smooth" });
      }
    });
  });

  if (document.cookie.includes("session=")) {
    fetch("/me", { credentials: "same-origin" })
      .then((res) => {
        if (res.ok) {
          document.querySelectorAll('a[href="/auth/google"]').forEach((btn) => {
            btn.href = "preferences";
            btn.innerHTML =
              'Go to Preferences <svg class="icon-ext" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><line x1="5" y1="12" x2="19" y2="12"></line><polyline points="12 5 19 12 12 19"></polyline></svg>';
          });
        }
      })
      .catch((err) => console.error(err));
  }
}

async function initPreferences() {
  let card = document.querySelector(".pref-card");
  if (card) {
    card.animate(
      [
        { opacity: 0, transform: "translateY(20px)" },
        { opacity: 1, transform: "translateY(0)" }
      ],
      {
        duration: 500,
        easing: "ease-out",
        fill: "forwards"
      }
    );
  }

  let existingPlatforms = [];
  try {
    let meRes = await fetch("/me", { credentials: "same-origin" });
    if (meRes.ok) {
      let me = await meRes.json();
      csrfToken = me.csrf_token || "";
      let emailEl = document.getElementById("user-email");
      if (emailEl && me.email) emailEl.textContent = me.email;
      existingPlatforms = me.platforms || [];
      let useDedicatedCheckbox = document.getElementById("use-dedicated");
      if (useDedicatedCheckbox) {
        useDedicatedCheckbox.checked = !!me.use_dedicated;
      }
    } else if (meRes.status === 401) {
      window.location.href = "/auth/google";
      return;
    }
  } catch (e) {
    console.error(e);
  }

  try {
    let pRes = await fetch("/platforms", { credentials: "same-origin" });
    let container = document.getElementById("platforms-list");
    if (!container) return;

    if (pRes.ok) {
      let data = await pRes.json();
      let platforms = data.platforms || [];

      let colorMap = {
        leetcode: "var(--platform-leetcode)",
        codeforces: "var(--platform-codeforces)",
        codechef: "var(--platform-codechef)",
        atcoder: "var(--platform-atcoder)",
        hackerrank: "var(--platform-hackerrank)",
        geeksforgeeks: "var(--platform-gfg)",
        code360: "var(--platform-code360)",
      };

      container.innerHTML = "";
      platforms.forEach((p) => {
        let label = document.createElement("label");
        label.className = "platform-item";
        let isChecked =
          existingPlatforms.length === 0 || existingPlatforms.includes(p);
        label.innerHTML = `
                    <input type="checkbox" name="platform" value="${p}" ${isChecked ? "checked" : ""}>
                    <div class="custom-checkbox"></div>
                    <div class="p-dot" style="background: ${colorMap[p] || "var(--text-dim)"}"></div>
                    <span class="p-label">${p.charAt(0).toUpperCase() + p.slice(1)}</span>
                `;
        container.appendChild(label);
      });
    }

    let form = document.getElementById("pref-form");
    if (form) {
      form.addEventListener("submit", async (e) => {
        e.preventDefault();
        let submitBtn = form.querySelector('button[type="submit"]');
        submitBtn.disabled = true;
        submitBtn.textContent = "Syncing...";

        let captchaResponse = hcaptcha.getResponse();
        if (!captchaResponse) {
          showToast("Please complete the CAPTCHA challenge.", "error");
          submitBtn.disabled = false;
          submitBtn.textContent = "Start Sync";
          return;
        }

        let selected = Array.from(
          document.querySelectorAll('input[name="platform"]:checked'),
        ).map((i) => i.value);
        let useDedicated = false;
        let useDedicatedCheckbox = document.getElementById("use-dedicated");
        if (useDedicatedCheckbox) {
          useDedicated = useDedicatedCheckbox.checked;
        }
        try {
          let res = await securePost("/preferences", {
            platforms: selected,
            use_dedicated: useDedicated,
            "h-captcha-response": captchaResponse,
          });
          hcaptcha.reset();
          if (res) {
            if (res.changed) {
              try {
                let syncRes = await securePost("/sync", {});
                if (syncRes && syncRes.status === "rate_limited") {
                  showSuccess(
                    "Preferences updated! Sync is rate-limited.",
                    "Since you synced recently, your new choices will be automatically updated on the next hourly schedule.",
                  );
                } else {
                  showSuccess(
                    "Preferences saved and sync queued!",
                    "Your calendar is being updated with the new platform selections.",
                  );
                }
              } catch (syncErr) {
                showSuccess(
                  "Preferences saved successfully.",
                  "Calendar updates will apply automatically on the next scheduled run.",
                );
              }
            } else {
              showSuccess(
                "Preferences saved!",
                "Your selections are already up to date. No new sync required.",
              );
            }
          }
        } catch (err) {
          hcaptcha.reset();
          showToast(err.message || "An error occurred.", "error");
          submitBtn.disabled = false;
          submitBtn.textContent = "Start Sync";
        }
      });
    }

    let delBtn = document.getElementById("delete-account-btn");
    if (delBtn) {
      delBtn.addEventListener("click", async () => {
        const deleteGoogleData = !!document.getElementById("delete-google-data")?.checked;
        let confirmMsg = "Are you sure? This will remove all your data and stop calendar syncing.";
        if (deleteGoogleData) {
          confirmMsg += "\n\nThis will ALSO delete your ContestSync calendar and all synced events from your Google Calendar, and revoke app access permanently.";
        }
        if (confirm(confirmMsg)) {
          try {
            const res = await fetch(`/account?delete_google_data=${deleteGoogleData}`, {
              method: "DELETE",
              headers: { "X-CSRF-Token": getCSRFToken() },
              credentials: "same-origin",
            });
            if (res.ok) {
              window.location.href = "/";
            } else {
              const data = await res.json().catch(() => ({}));
              alert(`Failed to delete account: ${data.error || res.statusText}`);
            }
          } catch (err) {
            console.error(err);
          }
        }
      });
    }
  } catch (e) {
    console.error(e);
  }
}

async function securePost(url, body) {
  let res = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": getCSRFToken(),
    },
    credentials: "same-origin",
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    let e = await res.json().catch(() => ({ error: "request failed" }));
    throw new Error(e.error);
  }
  return res.json();
}

function getCSRFToken() {
  return csrfToken;
}

function showSuccess(message, subMessage) {
  let card = document.querySelector(".pref-card");
  if (!card) return;

  let mainMsg = message || "Your sync request has been added to the queue.";
  let subMsg =
    subMessage ||
    "Contests will appear in your Google Calendar within a few minutes.";

  const fadeOut = card.animate(
    [
      { opacity: 1, transform: "translateY(0)" },
      { opacity: 0, transform: "translateY(-20px)" }
    ],
    {
      duration: 400,
      easing: "ease-in",
      fill: "forwards"
    }
  );
  fadeOut.onfinish = () => {
    card.innerHTML = `
      <div class="success-state centered">
          <div style="margin-bottom:1.5rem;">
              <svg width="56" height="56" viewBox="0 0 24 24" fill="none" stroke="var(--accent-cyan)" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
                  <polyline points="20 6 9 17 4 12"></polyline>
              </svg>
          </div>
          <h1>You're all set.</h1>
          <p>${mainMsg}</p>
          <p style="margin-top:0.5rem; font-size:var(--t-label); color:var(--text-dim);">${subMsg}</p>
          <div style="display:flex; gap:1rem; justify-content:center; flex-wrap:wrap; margin-top:1.5rem;">
              <a href="https://calendar.google.com" target="_blank" rel="noopener noreferrer" class="btn btn-primary">
                  Open Google Calendar
              </a>
              <a href="/" class="btn btn-ghost" style="padding:1rem 1.5rem; border:1px solid var(--bg-border); border-radius:8px;">
                  ← Back to Home
              </a>
          </div>
      </div>
    `;
    card.animate(
      [
        { opacity: 0, transform: "translateY(20px)" },
        { opacity: 1, transform: "translateY(0)" }
      ],
      {
        duration: 500,
        easing: "ease-out",
        fill: "forwards"
      }
    );
  };
}

function showToast(message, type = "success") {
  let container = document.querySelector(".toast-container");
  if (!container) {
    container = document.createElement("div");
    container.className = "toast-container";
    document.body.appendChild(container);
  }

  let toast = document.createElement("div");
  toast.className = `toast toast-${type}`;

  let icon = "";
  if (type === "success") {
    icon = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>`;
  } else if (type === "error") {
    icon = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>`;
  } else if (type === "info") {
    icon = `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>`;
  }

  toast.innerHTML = `
    <span class="toast-icon">${icon}</span>
    <span class="toast-message">${message}</span>
  `;
  container.appendChild(toast);

  toast.animate(
    [
      { opacity: 0, transform: "translateY(20px) scale(0.9)" },
      { opacity: 1, transform: "translateY(0) scale(1)" }
    ],
    {
      duration: 400,
      easing: "ease-out",
      fill: "forwards"
    }
  );

  setTimeout(() => {
    const fadeOut = toast.animate(
      [
        { opacity: 1, transform: "translateY(0) scale(1)" },
        { opacity: 0, transform: "translateY(-20px) scale(0.9)" }
      ],
      {
        duration: 400,
        easing: "ease-in",
        fill: "forwards"
      }
    );
    fadeOut.onfinish = () => toast.remove();
  }, 4000);
}

function initGlobalInteractivity() {
  let grid = document.querySelector(".hc-grid");
  if (!grid) {
    grid = document.createElement("div");
    grid.className = "hc-grid";
    document.body.prepend(grid);
  }
  let vignette = document.querySelector(".hc-vignette");
  if (!vignette) {
    vignette = document.createElement("div");
    vignette.className = "hc-vignette";
    document.body.prepend(vignette);
  }
  let scanlines = document.querySelector(".hc-scanlines");
  if (!scanlines) {
    scanlines = document.createElement("div");
    scanlines.className = "hc-scanlines";
    document.body.prepend(scanlines);
  }

  if (window.matchMedia("(hover: hover)").matches) {
    let cursor = document.querySelector(".custom-cursor");
    if (!cursor) {
      cursor = document.createElement("div");
      cursor.className = "custom-cursor";
      document.body.appendChild(cursor);
    }
    let cursorDot = document.querySelector(".custom-cursor-dot");
    if (!cursorDot) {
      cursorDot = document.createElement("div");
      cursorDot.className = "custom-cursor-dot";
      document.body.appendChild(cursorDot);
    }

    let mouseX = 0, mouseY = 0;
    let cursorX = 0, cursorY = 0;
    document.addEventListener("mousemove", (e) => {
      mouseX = e.clientX;
      mouseY = e.clientY;
      cursorDot.style.transform = `translate(${mouseX}px, ${mouseY}px)`;
    });

    function tickCursor() {
      const dx = mouseX - cursorX;
      const dy = mouseY - cursorY;
      cursorX += dx * 0.15;
      cursorY += dy * 0.15;
      cursor.style.transform = `translate(${cursorX}px, ${cursorY}px) scale(${cursor.dataset.scale || 1})`;
      requestAnimationFrame(tickCursor);
    }
    tickCursor();

    document.addEventListener("mousedown", () => {
      cursor.dataset.scale = "0.8";
    });

    document.addEventListener("mouseup", () => {
      cursor.dataset.scale = "1";
    });

    const addHoverListeners = () => {
      document
        .querySelectorAll(
          'a, button, .btn, .platform-card, .platform-item, .custom-checkbox, input[type="checkbox"]',
        )
        .forEach((el) => {
          if (!el.dataset.cursorBound) {
            el.dataset.cursorBound = "true";
            el.addEventListener("mouseenter", () =>
              cursor.classList.add("hover"),
            );
            el.addEventListener("mouseleave", () =>
              cursor.classList.remove("hover"),
            );
          }
        });
    };

    addHoverListeners();
    setInterval(addHoverListeners, 1000);
  }

  document.querySelectorAll(".platform-card").forEach((card) => {
    let rect = null;
    card.addEventListener("pointerenter", () => {
      rect = card.getBoundingClientRect();
    });
    card.addEventListener("mousemove", (e) => {
      if (!rect) rect = card.getBoundingClientRect();
      const x = e.clientX - rect.left;
      const y = e.clientY - rect.top;
      card.style.setProperty("--mx", `${x}px`);
      card.style.setProperty("--my", `${y}px`);

      const tiltX = x - rect.width / 2;
      const tiltY = y - rect.height / 2;
      card.style.transition = "transform 0.1s ease-out";
      card.style.transform = `rotateY(${tiltX * 0.08}deg) rotateX(${-tiltY * 0.08}deg)`;
    });

    card.addEventListener("mouseleave", () => {
      rect = null;
      card.style.transition = "transform 0.5s ease-out";
      card.style.transform = "rotateY(0deg) rotateX(0deg)";
    });
  });
}

function initFAQ() {
  document.querySelectorAll(".faq-item").forEach((item) => {
    const btn = item.querySelector(".faq-q");
    if (!btn) return;
    btn.addEventListener("click", () => {
      const isOpen = item.classList.contains("open");
      document.querySelectorAll(".faq-item").forEach((i) => {
        i.classList.remove("open");
        const q = i.querySelector(".faq-q");
        if (q) q.setAttribute("aria-expanded", "false");
      });
      if (!isOpen) {
        item.classList.add("open");
        btn.setAttribute("aria-expanded", "true");
      }
    });
  });
}

async function fetchGitHubStars() {
  const elMain = document.getElementById("github-stars-count");
  const elAbout = document.getElementById("github-stars-count-about");
  if (!elMain && !elAbout) return;
  try {
    const res = await fetch(
      "https://api.github.com/repos/0xarchit/contestSync",
    );
    if (res.ok) {
      const data = await res.json();
      if (data && typeof data.stargazers_count === "number") {
        if (elMain) elMain.textContent = data.stargazers_count;
        if (elAbout) elAbout.textContent = data.stargazers_count;
      }
    }
  } catch (e) {
    console.error(e);
  }
}

function queueGitHubStars() {
  if ("requestIdleCallback" in window) {
    requestIdleCallback(fetchGitHubStars);
  } else {
    setTimeout(fetchGitHubStars, 1);
  }
}

window.addEventListener("DOMContentLoaded", () => {
  initApp();
  initGlobalInteractivity();
  initFAQ();
  queueGitHubStars();
});

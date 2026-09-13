const tablist = document.querySelector('[role="tablist"]');
const tabs = [...tablist.querySelectorAll('[role="tab"]')];

function selectTab(selected) {
  for (const tab of tabs) {
    const active = tab === selected;
    tab.setAttribute("aria-selected", String(active));
    tab.tabIndex = active ? 0 : -1;
    document.getElementById(tab.getAttribute("aria-controls")).hidden = !active;
  }
}

for (const tab of tabs) {
  const panel = document.getElementById(tab.getAttribute("aria-controls"));
  panel.setAttribute("role", "tabpanel");
  panel.setAttribute("aria-labelledby", tab.id);
  panel.tabIndex = 0;
  tab.addEventListener("click", () => selectTab(tab));
  tab.addEventListener("keydown", (event) => {
    const index = tabs.indexOf(tab);
    let next;
    if (event.key === "ArrowRight") next = tabs[(index + 1) % tabs.length];
    if (event.key === "ArrowLeft") next = tabs[(index - 1 + tabs.length) % tabs.length];
    if (event.key === "Home") next = tabs[0];
    if (event.key === "End") next = tabs.at(-1);
    if (!next) return;
    event.preventDefault();
    selectTab(next);
    next.focus();
  });
}
selectTab(tabs[0]);
tablist.hidden = false;

if (navigator.clipboard?.writeText) {
  for (const button of document.querySelectorAll("[data-copy]")) {
    button.hidden = false;
    const label = button.getAttribute("aria-label");
    let reset;
    button.addEventListener("click", async () => {
      const status = document.getElementById("copy-status");
      const code = document.getElementById(button.dataset.copy);
      clearTimeout(reset);
      status.textContent = "";
      try {
        await navigator.clipboard.writeText(code.textContent);
        button.textContent = "Copied";
        button.setAttribute("aria-label", label.replace(/^Copy /, "Copied "));
        status.textContent = button.getAttribute("aria-label") + ".";
      } catch {
        button.textContent = "Copy failed";
        button.setAttribute("aria-label", label.replace(/^Copy /, "Copy failed: "));
        status.textContent = "Clipboard access is unavailable. Select and copy the command directly.";
      }
      reset = setTimeout(() => {
        button.textContent = "Copy";
        button.setAttribute("aria-label", label);
      }, 2500);
    });
  }
}

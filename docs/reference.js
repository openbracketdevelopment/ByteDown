const tabs = [...document.querySelectorAll("[data-tab]")];
function activate(tab) {
  for (const button of tabs) {
    const active = button === tab;
    button.setAttribute("aria-selected", String(active));
    button.tabIndex = active ? 0 : -1;
    document.getElementById(button.getAttribute("aria-controls")).hidden =
      !active;
  }
}
for (const [index, tab] of tabs.entries()) {
  tab.addEventListener("click", () => activate(tab));
  tab.addEventListener("keydown", (event) => {
    let target;
    if (event.key === "ArrowRight") target = tabs[(index + 1) % tabs.length];
    if (event.key === "ArrowLeft")
      target = tabs[(index - 1 + tabs.length) % tabs.length];
    if (event.key === "Home") target = tabs[0];
    if (event.key === "End") target = tabs.at(-1);
    if (target) {
      event.preventDefault();
      activate(target);
      target.focus();
    }
  });
}
for (const button of document.querySelectorAll(".copy")) {
  button.addEventListener("click", async () => {
    const code = button.parentElement.querySelector("code");
    try {
      await navigator.clipboard.writeText(code.textContent);
      button.textContent = "Copied ✓";
      document.getElementById("copy-status").textContent =
        "Example copied to clipboard.";
      setTimeout(() => {
        button.textContent = "Copy";
      }, 2000);
    } catch {
      document.getElementById("copy-status").textContent =
        "Clipboard unavailable. Select the example text to copy it.";
      code.parentElement.tabIndex = 0;
      code.parentElement.focus();
    }
  });
}

// Applied before first paint (external file: the panel CSP forbids inline scripts).
try {
  var theme = localStorage.getItem("ezhiklb-theme")
  if (theme !== "light" && theme !== "dark") theme = matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark"
  document.documentElement.dataset.theme = theme
} catch (error) {
  document.documentElement.dataset.theme = "dark"
}

import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import "@fontsource-variable/geist"
import "@fontsource-variable/geist-mono"
import App from "./App"
import "./tokens.css"
import "./styles.css"
import "./design.css"

createRoot(document.getElementById("root")!).render(<StrictMode><App /></StrictMode>)


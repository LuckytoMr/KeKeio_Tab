import { render } from "preact";
import { App } from "./app";
import "./styles.css";

const root = document.querySelector<HTMLDivElement>("#app");
if (!root) throw new Error("kekeio admin mount point is missing");
render(<App />, root);

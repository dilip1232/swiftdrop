import { mount } from "svelte";
import "./styles/global.css";
import App from "./App.svelte";

// Prevent the browser from navigating when files are dropped outside the drop zone.
document.addEventListener("dragover", e => e.preventDefault());
document.addEventListener("drop", e => e.preventDefault());

const app = mount(App, {
  target: document.getElementById("app")!,
});

export default app;

"use strict";

const dot = document.getElementById("dot");
const statusText = document.getElementById("statusText");
const errorEl = document.getElementById("error");
const tabEl = document.getElementById("tab");
const portInput = document.getElementById("port");
const toggleBtn = document.getElementById("toggle");

let connected = false;

function send(msg) {
  return new Promise((resolve) => {
    chrome.runtime.sendMessage(msg, (resp) => resolve(resp));
  });
}

function render(status, tab) {
  dot.className = "dot " + (status.connected ? "ok" : status.connecting ? "warn" : "err");
  statusText.textContent = status.connected ? "Connected" : status.connecting ? "Connecting…" : "Disconnected";
  errorEl.textContent = status.error || "";
  if (tab) {
    tabEl.textContent = tab.title + "\n" + tab.url;
  } else {
    tabEl.textContent = "";
  }
  connected = status.connected;
  toggleBtn.textContent = connected ? "Disconnect" : "Connect";
  portInput.disabled = connected;
}

async function refresh() {
  const resp = await send({ type: "getStatus" });
  if (resp && resp.status) render(resp.status, resp.tab);
}

toggleBtn.addEventListener("click", async () => {
  if (connected) {
    await send({ type: "disconnect" });
  } else {
    const port = parseInt(portInput.value, 10) || 18765;
    await send({ type: "setConfig", port });
  }
  setTimeout(refresh, 300);
});

portInput.addEventListener("change", () => {
  const port = parseInt(portInput.value, 10) || 18765;
  portInput.value = port;
});

document.addEventListener("DOMContentLoaded", refresh);
setInterval(refresh, 1000);
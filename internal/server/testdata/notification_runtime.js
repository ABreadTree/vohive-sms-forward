const fs = require("fs");
const vm = require("vm");

async function main() {
  const { block, scenario } = JSON.parse(fs.readFileSync(0, "utf8"));
  const elements = new Map();
  const get = id => {
    if (!elements.has(id)) elements.set(id, {
      value: "", textContent: "", disabled: false, className: "",
      handlers: {}, addEventListener(type, handler) { this.handlers[type] = handler; }
    });
    return elements.get(id);
  };
  const pending = {};
  const context = vm.createContext({
    document: { getElementById: get },
    $: get,
    fetch: path => new Promise(resolve => { pending[path] = resolve; })
  });
  vm.runInContext(block, context);
  if (scenario === "file_error") {
    vm.runInContext('renderNotificationStatus({error:"config_invalid",bark:{configured:false,active:false},telegram:{configured:false,active:false}})', context);
  } else if (scenario === "out_of_order") {
    get("bark-server").value = "https://api.day.app";
    get("bark-key").value = "key";
    get("tg-token").value = "token";
    get("tg-chat-id").value = "123";
    const e = { preventDefault() {} };
    const bark = get("bark-form").handlers.submit(e);
    const telegram = get("telegram-form").handlers.submit(e);
    pending["/api/notifications/telegram"]({ ok: true, json: async () => ({
      bark: { configured: false, active: false }, telegram: { configured: true, active: true }
    }) });
    await telegram;
    pending["/api/notifications/bark"]({ ok: true, json: async () => ({
      bark: { configured: true, active: true }, telegram: { configured: false, active: false }
    }) });
    await bark;
  } else {
    throw new Error("unknown scenario");
  }
  process.stdout.write(JSON.stringify({
    aggregate: get("notification-status").textContent,
    bark: get("bark-status").textContent,
    telegram: get("telegram-status").textContent,
    telegramTestDisabled: get("test-telegram").disabled
  }));
}
main().catch(e => { process.stderr.write(String(e)); process.exitCode = 1; });

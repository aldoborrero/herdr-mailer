import { readConfig, startBridge, content } from "./bridge.js";

export default function herdrMailer(pi) {
  let bridge;
  const stop = () => {
    bridge?.stop();
    bridge = undefined;
  };
  pi.on("session_shutdown", stop);
  pi.on("session_start", (_event, ctx) => {
    stop();
    const config = readConfig();
    if (config.runtime !== "pi")
      throw new Error("expected Pi bridge configuration");
    const isCurrent = () =>
      ctx.sessionManager.getSessionId() === config.location;
    if (!isCurrent()) {
      ctx.ui.notify(
        "herdr-mailer: session UUID differs; register this session before connecting",
        "warning",
      );
      return;
    }
    bridge = startBridge(
      config,
      (message) => {
        // The public API returns void. This receipt proves API submission only;
        // later provider/model errors and responses remain independent.
        pi.sendMessage(
          {
            customType: "herdr-mailer",
            content: content(message),
            display: true,
            details: {
              origin: `${message.owner}/${message.author}`,
              id: message.id,
            },
          },
          { triggerTurn: true, deliverAs: "followUp" },
        );
        return "accepted";
      },
      { isCurrent },
    );
  });
}

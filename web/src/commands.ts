// Slash commands available in the GUI. Names match the TUI's builtinCommands
// where the concept carries over, so muscle memory transfers between the two.
export type Command = {
  name: string;
  arg?: string;
  desc: string;
};

export const COMMANDS: Command[] = [
  { name: "/new", desc: "start a new thread" },
  { name: "/clear", desc: "clear the transcript (keeps the thread)" },
  { name: "/model", arg: "[name]", desc: "switch model, or open the picker" },
  { name: "/models", desc: "open the model picker" },
  { name: "/effort", arg: "[level]", desc: "set reasoning effort" },
  { name: "/theme", arg: "[name]", desc: "cycle or set the theme" },
  { name: "/threads", desc: "toggle the thread sidebar" },
  { name: "/sessions", desc: "toggle the thread sidebar" },
  { name: "/skills", desc: "list available skills" },
  {
    name: "/review",
    arg: "[base]",
    desc: "review this branch against its base",
  },
  { name: "/settings", desc: "open settings" },
  { name: "/provider", desc: "manage provider sign-in" },
  { name: "/providers", desc: "manage provider sign-in" },
  { name: "/stats", desc: "toggle the activity panel" },
  { name: "/tools", desc: "show or hide tool cards" },
  { name: "/copy", desc: "copy the last response" },
  {
    name: "/yolo",
    arg: "[on|off]",
    desc: "run tools without asking for approval",
  },
  { name: "/cancel", desc: "cancel the running turn" },
  { name: "/help", desc: "show commands and shortcuts" },
];

// matchCommands filters the palette for what the user has typed so far. A bare
// "/" lists everything; skills are appended so /skill-name completes too.
export function matchCommands(
  input: string,
  skills: { name: string; description?: string }[],
): Command[] {
  if (!input.startsWith("/")) return [];
  const q = input.slice(1).toLowerCase().split(" ")[0];
  const all: Command[] = [
    ...COMMANDS,
    ...skills.map((s) => ({
      name: "/" + s.name,
      desc: s.description || "skill",
    })),
  ];
  if (!q) return all;
  return all.filter((c) => c.name.slice(1).toLowerCase().includes(q));
}

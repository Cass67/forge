// Injected by forge's preview proxy into the app being previewed.
//
// It does three things for the pane that frames it: reports console errors as
// they happen, lets the user point at an element to describe it, and rasterises
// the viewport so the agent gets a picture as well as a selector. It talks to
// the pane over postMessage and touches nothing else in the page.
(function () {
  "use strict";
  if (window.__forgePreviewBridge) return;
  window.__forgePreviewBridge = true;

  var CHANNEL = "forge-preview";
  var picking = false;
  var outline = null;

  function post(type, payload) {
    try {
      parent.postMessage(
        { channel: CHANNEL, type: type, payload: payload },
        "*",
      );
    } catch (err) {
      /* the pane went away */
    }
  }

  // ---- console and error capture -----------------------------------------
  function report(level, parts) {
    var text = parts
      .map(function (part) {
        if (part instanceof Error) return part.stack || part.message;
        if (typeof part === "string") return part;
        try {
          return JSON.stringify(part);
        } catch (err) {
          return String(part);
        }
      })
      .join(" ");
    post("log", { level: level, text: text.slice(0, 2000), at: Date.now() });
  }

  ["error", "warn"].forEach(function (level) {
    var original = console[level];
    console[level] = function () {
      report(level, Array.prototype.slice.call(arguments));
      return original.apply(console, arguments);
    };
  });

  window.addEventListener("error", function (event) {
    var where = event.filename
      ? " (" + event.filename + ":" + event.lineno + ")"
      : "";
    report("error", [(event.message || "script error") + where]);
  });

  window.addEventListener("unhandledrejection", function (event) {
    report("error", ["unhandled promise rejection:", event.reason]);
  });

  // ---- describing an element ---------------------------------------------
  // A selector the user can paste into devtools: ids short-circuit the walk,
  // everything else is positioned among its siblings so it stays unambiguous.
  function selectorFor(element) {
    var parts = [];
    var node = element;
    while (node && node.nodeType === 1 && parts.length < 8) {
      if (node.id) {
        parts.unshift("#" + CSS.escape(node.id));
        break;
      }
      var name = node.tagName.toLowerCase();
      var parent = node.parentElement;
      if (parent) {
        var twins = Array.prototype.filter.call(
          parent.children,
          function (child) {
            return child.tagName === node.tagName;
          },
        );
        if (twins.length > 1)
          name += ":nth-of-type(" + (twins.indexOf(node) + 1) + ")";
      }
      parts.unshift(name);
      node = node.parentElement;
    }
    return parts.join(" > ");
  }

  var STYLE_KEYS = [
    "display",
    "position",
    "width",
    "height",
    "margin",
    "padding",
    "color",
    "background-color",
    "font-family",
    "font-size",
    "font-weight",
    "border",
    "border-radius",
    "flex",
    "grid-template-columns",
    "gap",
    "opacity",
    "overflow",
    "z-index",
  ];

  function describe(element) {
    var computed = getComputedStyle(element);
    var styles = {};
    STYLE_KEYS.forEach(function (key) {
      var value = computed.getPropertyValue(key);
      if (value) styles[key] = value.trim();
    });
    var box = element.getBoundingClientRect();
    return {
      selector: selectorFor(element),
      tag: element.tagName.toLowerCase(),
      id: element.id || "",
      classes:
        element.className && element.className.baseVal === undefined
          ? String(element.className)
          : "",
      text: (element.textContent || "").trim().slice(0, 300),
      html: element.outerHTML.slice(0, 1200),
      box: {
        x: Math.round(box.x),
        y: Math.round(box.y),
        width: Math.round(box.width),
        height: Math.round(box.height),
      },
      styles: styles,
      url: location.href,
      viewport: { width: innerWidth, height: innerHeight },
    };
  }

  // ---- rasterising the viewport ------------------------------------------
  // The DOM is cloned into an SVG foreignObject and drawn to a canvas: no
  // rendering library, and close enough for the agent to see what the user
  // sees. Cross-origin images taint the canvas, so a failure here is expected
  // and reported rather than treated as an error.
  function stylesheetText() {
    return Array.prototype.map
      .call(document.styleSheets, function (sheet) {
        try {
          return Array.prototype.map
            .call(sheet.cssRules, function (rule) {
              return rule.cssText;
            })
            .join("\n");
        } catch (err) {
          return ""; // cross-origin stylesheet, unreadable by design
        }
      })
      .join("\n");
  }

  function screenshot() {
    return new Promise(function (resolve) {
      try {
        var width = Math.min(innerWidth, 1600);
        var height = Math.min(innerHeight, 1200);
        var clone = document.documentElement.cloneNode(true);
        Array.prototype.forEach.call(
          clone.querySelectorAll("script,link[rel=stylesheet]"),
          function (node) {
            node.remove();
          },
        );
        var style = document.createElement("style");
        style.textContent = stylesheetText();
        clone.querySelector("head")?.appendChild(style);
        var body = clone.querySelector("body");
        if (body)
          body.style.transform =
            "translate(" + -scrollX + "px," + -scrollY + "px)";

        var svg =
          '<svg xmlns="http://www.w3.org/2000/svg" width="' +
          width +
          '" height="' +
          height +
          '">' +
          '<foreignObject width="100%" height="100%">' +
          '<div xmlns="http://www.w3.org/1999/xhtml">' +
          clone.outerHTML +
          "</div>" +
          "</foreignObject></svg>";

        var image = new Image();
        var svgURL =
          "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg);
        image.onload = function () {
          try {
            var canvas = document.createElement("canvas");
            canvas.width = width;
            canvas.height = height;
            var context = canvas.getContext("2d");
            context.fillStyle =
              getComputedStyle(document.body).backgroundColor || "#fff";
            context.fillRect(0, 0, width, height);
            context.drawImage(image, 0, 0);
            resolve({ image: canvas.toDataURL("image/png") });
          } catch (err) {
            resolve({ error: String(err) });
          }
        };
        image.onerror = function () {
          resolve({ error: "the page could not be rasterised" });
        };
        image.src = svgURL;
      } catch (err) {
        resolve({ error: String(err) });
      }
    });
  }

  // ---- pick mode ----------------------------------------------------------
  function ensureOutline() {
    if (outline) return outline;
    outline = document.createElement("div");
    outline.style.cssText =
      "position:fixed;z-index:2147483647;pointer-events:none;border:2px solid #2563eb;" +
      "background:rgba(37,99,235,0.12);border-radius:2px;transition:all 40ms linear;";
    document.body.appendChild(outline);
    return outline;
  }

  function moveOutline(element) {
    var box = element.getBoundingClientRect();
    var node = ensureOutline();
    node.style.display = "block";
    node.style.left = box.left + "px";
    node.style.top = box.top + "px";
    node.style.width = box.width + "px";
    node.style.height = box.height + "px";
  }

  function onMove(event) {
    if (!picking) return;
    var element = event.target;
    if (element && element.nodeType === 1 && element !== outline)
      moveOutline(element);
  }

  function onClick(event) {
    if (!picking) return;
    event.preventDefault();
    event.stopPropagation();
    var element = event.target;
    setPicking(false);
    var described = describe(element);
    screenshot().then(function (shot) {
      described.screenshot = shot.image || "";
      described.screenshotError = shot.error || "";
      post("picked", described);
    });
  }

  function onKey(event) {
    if (picking && event.key === "Escape") {
      event.preventDefault();
      setPicking(false);
      post("cancelled", null);
    }
  }

  function setPicking(on) {
    picking = on;
    if (!on && outline) outline.style.display = "none";
    document.documentElement.style.cursor = on ? "crosshair" : "";
    post("picking", { on: on });
  }

  document.addEventListener("mousemove", onMove, true);
  document.addEventListener("click", onClick, true);
  document.addEventListener("keydown", onKey, true);

  window.addEventListener("message", function (event) {
    var data = event.data;
    if (!data || data.channel !== CHANNEL) return;
    if (data.type === "pick") setPicking(!!data.on);
    if (data.type === "capture") {
      screenshot().then(function (shot) {
        post("captured", {
          screenshot: shot.image || "",
          screenshotError: shot.error || "",
          url: location.href,
        });
      });
    }
  });

  post("ready", { url: location.href });
})();

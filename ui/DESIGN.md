---
name: CTech Account
description: Identity console for the aoctech.app platform — secure, modern, approachable. One cobalt accent, flat tonal surfaces, Geist everywhere.
colors:
  primary: "oklch(0.55 0.22 258)"
  primary-foreground: "oklch(0.99 0.01 255)"
  primary-dark: "oklch(0.68 0.18 258)"
  background: "oklch(0.985 0.01 255)"
  background-dark: "oklch(0.16 0.02 258)"
  foreground: "oklch(0.18 0.02 255)"
  foreground-dark: "oklch(0.97 0.01 255)"
  card: "oklch(1 0 0)"
  card-dark: "oklch(0.20 0.02 258)"
  popover: "oklch(1 0 0)"
  popover-dark: "oklch(0.20 0.02 258)"
  secondary: "oklch(0.96 0.02 255)"
  secondary-dark: "oklch(0.26 0.02 258)"
  muted: "oklch(0.97 0.015 255)"
  muted-dark: "oklch(0.24 0.02 258)"
  muted-foreground: "oklch(0.50 0.03 255)"
  muted-foreground-dark: "oklch(0.72 0.02 255)"
  accent: "oklch(0.94 0.03 255)"
  accent-dark: "oklch(0.28 0.03 258)"
  destructive: "oklch(0.55 0.245 27.325)"
  destructive-dark: "oklch(0.704 0.191 22.216)"
  border: "oklch(0.92 0.02 255)"
  border-dark: "oklch(0.30 0.02 258)"
  input: "oklch(0.92 0.02 255)"
  input-dark: "oklch(0.30 0.02 258)"
  ring: "oklch(0.62 0.2 258)"
  ring-dark: "oklch(0.68 0.18 258)"
typography:
  display:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.5rem"
    fontWeight: 600
    lineHeight: 1.25
    letterSpacing: "-0.02em"
  title:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1rem"
    fontWeight: 500
    lineHeight: 1.4
  body:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 500
  caption:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.8rem"
    fontWeight: 500
  mono:
    fontFamily: "Geist Mono, ui-monospace, SFMono-Regular, monospace"
    fontSize: "0.875rem"
    fontWeight: 400
rounded:
  sm: "0.525rem"
  md: "0.7rem"
  lg: "0.875rem"
  xl: "1.225rem"
  pill: "2.275rem"
spacing:
  sm: "0.5rem"
  md: "1rem"
  lg: "1.5rem"
components:
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.primary-foreground}"
    rounded: "{rounded.lg}"
    height: "2rem"
    padding: "0 0.625rem"
    typography: "{typography.label}"
  button-primary-hover:
    backgroundColor: "oklch(0.55 0.22 258 / 0.8)"
    textColor: "{colors.primary-foreground}"
    rounded: "{rounded.lg}"
    height: "2rem"
    padding: "0 0.625rem"
  button-outline:
    backgroundColor: "{colors.background}"
    textColor: "{colors.foreground}"
    rounded: "{rounded.lg}"
    height: "2rem"
    padding: "0 0.625rem"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.foreground}"
    rounded: "{rounded.lg}"
    height: "2rem"
    padding: "0 0.625rem"
  input:
    backgroundColor: "transparent"
    textColor: "{colors.foreground}"
    rounded: "{rounded.lg}"
    height: "2rem"
    padding: "0.25rem 0.625rem"
    typography: "{typography.body}"
  card:
    backgroundColor: "{colors.card}"
    rounded: "{rounded.xl}"
    padding: "1rem"
  badge:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.primary-foreground}"
    rounded: "{rounded.pill}"
    height: "1.25rem"
    padding: "0 0.5rem"
    typography: "{typography.label}"
  alert:
    backgroundColor: "{colors.card}"
    textColor: "{colors.card-foreground}"
    rounded: "{rounded.lg}"
    padding: "0.5rem 0.625rem"
---

# Design System: CTech Account

## 1. Overview

**Creative North Star: "The Quiet Vault"**

CTech Account is the identity console for the aoctech.app platform — where people sign in, enforce MFA and passkeys, review sessions and consents, verify identity (KYC), and where developers register OAuth apps and API keys. The design is a vault you don't notice: security is present on every surface, but it reads as calm competence, never as a wall of warnings. Two audiences share one console — account owners who are not security experts, and integrators who need depth — so the system stays restrained and consistent, letting the task lead.

This system explicitly rejects the PRODUCT.md anti-references: **Generic pastel SaaS** (airy, thin, substanceless), **Gamified consumer** (playful, cartoonish, reward-driven), **Dense enterprise gray** (cramped, gray, legacy back-office), and **Dark-mode hacker** (heavy dark-only terminal aesthetics that exclude non-technical users). The console is light-first with a faithful dark mode; never terminal-only.

**Key Characteristics:**
- One saturated accent — CTech Cobalt — used sparingly for primary actions, current selection, and security state. Everything else is neutral.
- Flat by default. Depth comes from 1px tonal rings and a faint blue backdrop, not drop shadows.
- A single, well-tuned sans (Geist) carries the whole UI; Geist Mono reserves itself for secrets, tokens, and IDs.
- Security states (verified, pending, revoked, linked) are always icon + text, never color alone.
- Bilingual (en / pt-BR); layout and copy hold both without truncation.

## 2. Colors: The CTech Cobalt System

One accent, a cool near-neutral ramp, and a strict semantic vocabulary. Cobalt anchors trust; neutrals do the work. (Values are OKLCH — the project's canonical color space. Stitch's hex linter will warn; OKLCH is the source of truth in `globals.css`.)

### Primary
- **CTech Cobalt** (oklch(0.55 0.22 258)): the single brand accent. Primary buttons, active nav item, focused selection, links, and positive security state (verified / MFA on). Used on ≤10% of any screen — its rarity is the point.
- **CTech Cobalt Dark** (oklch(0.68 0.18 258)): the same hue lifted for dark mode, where the lighter value keeps contrast against near-black surfaces.

### Neutral
- **Vault White** (oklch(0.985 0.01 255)): app background (light). A near-white with a whisper of cool blue, not a warm paper.
- **Ink** (oklch(0.18 0.02 255)): primary text (light).
- **Surface** (oklch(1 0 0)): cards and popovers — pure white, lifted only by a 1px ring.
- **Muted Surface** (oklch(0.97 0.015 255)): secondary fills, tab lists, hover wells.
- **Muted Ink** (oklch(0.50 0.03 255)): secondary text, captions. Must hold ≥4.5:1 on its backgrounds — never a lighter gray.
- **Hairline** (oklch(0.92 0.02 255)): borders and input strokes (light).

### Semantic
- **Alert Red** (oklch(0.55 0.245 27.325)): destructive actions, errors, revoked/expired state. Used as a tint (10% fill + saturated text), never a solid red button except where destruction is the explicit intent.
- **Focus Ring** (oklch(0.62 0.2 258)): the cobalt-derived focus ring; always visible at 3px / 70% alpha on keyboard focus.

Dark mode mirrors the same roles against **Obsidian** (oklch(0.16 0.02 258)) background and **Cloud** (oklch(0.97 0.01 255)) text; see frontmatter `*-dark` tokens.

### Named Rules
**The One Accent Rule.** CTech Cobalt appears on ≤10% of any given screen — primary actions, the active selection, and security-affirming state only. It is never decoration, never a background wash, never a gradient.

**The No-Spread Rule.** Depth is a 1px ring of `foreground/10`, not a box-shadow. Drop shadows are forbidden on surfaces at rest.

## 3. Typography

**Display Font:** Geist (with ui-sans-serif, system-ui fallback)
**Body Font:** Geist (same family)
**Label/Mono Font:** Geist Mono (with ui-monospace fallback) — secrets, tokens, client IDs, CPF masks

**Character:** One humanist sans, tuned tight. Geist carries headings, body, labels, and data with a single voice; the mono face is the only specialist, reserved for machine-readable strings so they never get mistaken for prose. No display/serif pairing — product UI earns familiarity through consistency, not contrast.

### Hierarchy
- **Display** (600, 1.5rem / 24px, line-height 1.25, letter-spacing -0.02em): page and section titles (e.g. account area headers). Fixed rem, not fluid.
- **Title** (500, 1rem / 16px, line-height 1.4): card titles, dialog titles.
- **Body** (400, 0.875rem / 14px, line-height 1.5): default text, table cells, descriptions. Prose capped at 65–75ch.
- **Label** (500, 0.875rem / 14px): form labels, badges, tab triggers, button text.

### Named Rules
**The One-Family Rule.** Geist everywhere; Geist Mono only for values a machine parses. Never introduce a second display face.

**The Fixed-Scale Rule.** Sizes are fixed rem on a tight 1.125–1.2 ratio. No clamp() fluid headings — a sidebar title that shrinks looks broken, not responsive.

## 4. Elevation

This system is flat. Cards and popovers sit on pure-white `Surface` lifted only by a 1px ring of `foreground/10`; the global backdrop is a faint vertical blue gradient (background → background → blue-50 / blue-950 in dark). Shadows are reserved for one transient moment: a dialog's open animation (zoom-in-95 + fade). Nothing else casts a shadow at rest.

### Named Rules
**The Flat-By-Default Rule.** Surfaces are flat at rest. The only "lift" is the 1px ring and the dialog's open transition. If you reach for `box-shadow` on a card, stop.

**The Tint-Not-Shadow Rule.** Hover and selected states change background tone (muted wells, selected row) — they do not add shadow.

## 5. Components

### Buttons
- **Shape:** gently rounded (14px / `rounded-lg`), height 32px, text-sm medium.
- **Primary:** CTech Cobalt fill, white text; hover → cobalt at 80% alpha (no darkening shift); active → 1px downward translate. Focus-visible → 3px cobalt ring at 70%.
- **Outline / Secondary / Ghost:** white or transparent fill, ink text; hover → muted well. Outline carries a hairline border.
- **Destructive:** red tint (10% fill) + red text; never a solid red block except for explicit irreversible actions.
- **Disabled:** 50% opacity, no pointer events.
- Every variant shares the same shape and focus treatment — see The One Vocabulary Rule.

### Cards / Containers
- **Corner:** 1.225rem (`rounded-xl`).
- **Background:** Surface (pure white); border replaced by `ring-1 ring-foreground/10`.
- **Internal padding:** 1rem (0.75rem when `size="sm"`).
- Holds header, description, content, footer; footer carries a muted top divider.

### Inputs / Fields
- **Shape:** 14px radius, 32px height, hairline `input` border, transparent fill.
- **Focus:** border shifts to cobalt + 3px cobalt ring at 70%.
- **Error:** `aria-invalid` → destructive border + destructive ring tint; message in red text below.
- **Disabled:** muted fill, 50% opacity, not-allowed cursor.

### Badges (status pills)
- **Style:** pill radius (2.275rem), height 20px, text-xs medium. Default = cobalt fill; secondary = muted; destructive = red tint + red text; outline = hairline border.
- Used for account state: Verified, Pending, Linked, Revoked.

### Alerts
- **Style:** 14px radius, hairline border, Surface fill. Destructive variant uses red text on Surface (no red fill flood). Title medium, description muted, balanced wrap.

### Tables
- **Style:** text-sm, header row bottom hairline, rows hover to muted well (`muted/50`), selected row muted. Horizontal-scroll container for narrow viewports. Used for sessions, API keys, OAuth clients, activity.

### Tabs
- **Style:** list is a muted pill (`rounded-lg`, 3px inset); trigger `rounded-md`, inactive ink at 60%, active = Surface fill + ink. `line` variant drops the pill for an underline. Keyboard focus → cobalt ring.

### Dialog
- **Overlay:** black at 10% + `backdrop-blur-xs`.
- **Surface:** Surface popover, 1.225rem radius, `ring-1 ring-foreground/10`, max-width ~448px (`sm`), zoom-in-95 + fade on open. Footer carries muted top divider; close button top-right ghost icon.

### Confirm Dialog (signature)
- **Role:** the branded, accessible replacement for native `confirm()` — the single gate for every destructive flow in the console: revoke session, revoke all sessions, revoke API key, delete OAuth client, regenerate client secret, unlink Google. Seven sites unified into one component; no native `confirm()` remains.
- **Composition:** reuses the Dialog surface. The destructive variant leads the title with an `AlertTriangle` icon (red), so the warning reads icon + text — never color alone. Footer = `Cancel` (outline) + the action (destructive red-tint) button.
- **State:** confirming sets `pending` — both buttons disable until the mutation resolves, and overlay/Esc close is suppressed mid-flight. Focus is trapped by the Dialog; first focus lands on `Cancel`, not the destructive action.

### Navigation (signature)
- **Style:** sticky top bar (h-14) + left sidebar (w-52, hidden on mobile). Active item = cobalt text + cobalt-tinted well; current section underlined in cobalt. Mobile collapses sidebar to a menu. The same vocabulary as tabs/buttons.

## 6. Do's and Don'ts

### Do:
- Do keep CTech Cobalt to primary actions, the active selection, and security-affirming state (≤10% of screen).
- Do convey security state with icon + text (Verified ✓, Pending, Revoked) — never color alone.
- Do use Geist Mono for secrets, tokens, client IDs, and CPF masks.
- Do respect the fixed rem type scale; let the sidebar and tables run dense when the task needs it.
- Do keep the flat, ring-based elevation; use muted wells for hover/selected, not shadows.
- Do hold ≥4.5:1 contrast for body and muted text; bump muted ink toward Ink if close.
- Do support both en and pt-BR without truncation or layout shift.

### Don't:
- Don't build a **Generic pastel SaaS** look — airy, thin, substanceless. This is a vault, not a landing page.
- Don't make it **Gamified consumer** — no playful mascots, reward badges, or cartoonish states.
- Don't ship **Dense enterprise gray** — cramped, flat-gray, legacy back-office density. Neutrals are cool and quiet, not dead gray.
- Don't go **Dark-mode hacker** — no terminal-only dark aesthetic that excludes non-technical users. Dark mode is a faithful twin of light, not the default.
- Don't add drop shadows to cards, inputs, or tables at rest (The Flat-By-Default Rule).
- Don't invent a second display font or pair Geist with a decorative face.
- Don't use `border-left`/`border-right` >1px as a colored stripe on cards, list rows, or alerts.
- Don't use gradient text or a colored gradient wash for the accent.
- Don't reach for a modal as the first response — exhaust inline and progressive disclosure first.
- Don't reinvent standard affordances (custom scrollbars, non-standard modals, weird form controls).
- Don't put heavy full-saturation cobalt on inactive/disabled states.

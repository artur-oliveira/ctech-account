---
target: src/app/account/identity/page.tsx (KYC flow)
total_score: 21
p0_count: 1
p1_count: 2
timestamp: 2026-07-15T23-58-29Z
slug: ui-src-app-account-identity-page-tsx
---
Method: dual-agent (A: design-review general-purpose · B: detector+browser-evidence general-purpose)

## Design Health Score

| # | Heuristic | Score | Key Issue |
|---|-----------|-------|-----------|
| 1 | Visibility of System Status | 2/4 | 4-dot selfie indicator is `aria-hidden` and there is no page-wide "3 of 6 complete" status — only a static one-line reminder |
| 2 | Match System / Real World | 3/4 | CPF masking, ViaCEP auto-fill, Brazilian address shape match local mental models well |
| 3 | User Control and Freedom | 1/4 | No delete/retake for an uploaded document or clip; recording auto-stops with no cancel; no way to skip/defer |
| 4 | Consistency and Standards | 3/4 | Uniform ShadCN inputs/spacing throughout — confirmed visually, no template drift |
| 5 | Error Prevention | 2/4 | Client-side file size/type checks are solid; no preview/confirm step before a bad photo or clip is accepted as final |
| 6 | Recognition Rather Than Recall | 3/4 | Form rehydrates `legal_name`/`birth_date` on retry |
| 7 | Flexibility and Efficiency | 2/4 | No power-user path, but nothing actively obstructs either |
| 8 | Aesthetic and Minimalist Design | 1/4 | Confirmed on screen: 4 distinct sub-flows (ID upload, biometric capture, personal info, address) share one undifferentiated bordered block with no dividers |
| 9 | Error Recovery | 2/4 | Slug→message mapping and `cameraDenied` copy are good, but rejection copy says "documents" while silently wiping biometric progress too |
| 10 | Help and Documentation | 1/4 | No consent/purpose copy before camera access; no upfront preview of the 6-item requirement |

**Total: 21/40 — Acceptable band, low end.** Significant improvements needed before users are happy with this flow, but nothing is broken.

## Anti-Patterns Verdict

**Not AI slop.** The deterministic detector (`detect.mjs`) returned zero findings across all three files — no gradient text, no side-stripe borders, no banned patterns. Both the source-level review and the live-screenshot review independently agree: this reads as deliberately engineered (MFA pre-gate, incremental server-persisted upload progress, ViaCEP integration, liveness-detection rationale documented in code comments), not generic AI output.

The failure mode here is the opposite of slop: the page is so undecorated that four logically distinct phases — upload ID, record biometric clips, enter personal info, enter address — render as one continuous gray-bordered block with no section headers, dividers, or step indicator. Two of the four phases ("Look up, then start recording" and "Address") get a real heading; ID-upload and personal-info entry get none at all — confirmed directly in the desktop screenshot.

**Deterministic scan**: `[]`, exit 0 — clean, no false positives to reconcile.

**Browser evidence**: 5 live screenshots captured (mock-mode dev server, real ShadCN/Tailwind rendering) across not-started/no-MFA, full-form (desktop + mobile), rejected, and verified states. No overlay injection was run (this was a direct Playwright capture rather than the detect.js browser-console flow); findings below come from direct visual inspection by an isolated second reviewer.

## Direct Answer: Is the KYC Too Aggressive?

**Partially — and now confirmed on real pixels, not just source.** The requirement set itself (CPF + address + 2 ID photos + 4-pose liveness clips) is not excessive for a regulated OIDC identity provider, and the liveness-clip design is explicitly justified in code against printed-photo/looped-video spoofing. That should stay.

What actually reads as aggressive:
- **The camera activates with zero upfront disclosure.** `getUserMedia` fires the instant the component mounts (`selfie-capture.tsx:50-60`) — confirmed on screen: the live camera box is the single largest, most visually loud element on the first mobile screen (~20-25% of the viewport), sitting above every personal-info field, with no privacy/consent copy anywhere nearby. The only adjacent text explains the liveness-detection *mechanism*, not data handling.
- **Rejection silently wipes more than it discloses.** Per the product's own README ("A rejection clears the uploaded documents, so resubmission requires a fresh [upload]"), a rejection for *any* reason — even "photo is blurry" — forces the user to re-shoot all 4 biometric clips, not just re-upload the flagged document. The rejected-state screenshot shows the UI copy saying only "Upload fresh documents and submit again," with the camera and progress dots silently reset and no acknowledgment that biometric re-capture is also required. A user reading that copy could reasonably believe only the photo needs replacing.
- **No sense of progress or "almost done."** Six required items, one static sentence, no checklist — every phase feels like starting from zero.
- **Structural flatness amplifies all of the above.** Nothing visually separates "give us your camera" from "give us your CPF" from "give us your address" — it's one scroll, so the biometric ask doesn't even get to breathe as its own deliberate step.

None of these require weakening the actual verification. They're disclosure, sequencing, and feedback problems — fixable without touching KYC rigor.

## What's Working

- **`MFARequired` gate** (`page.tsx:335-351`) stops a user from filling the entire form only to hit an uncrossable 403 later — confirmed clean on screen (screenshot 2), with a single clear CTA to enroll MFA first.
- **Incremental, server-persisted document/selfie progress** — a refresh mid-flow doesn't lose already-uploaded docs (`uploadedTypes` drives which pose renders next and gates submit). A genuine save-and-resume, though never communicated to the user as one.
- **Visual consistency**: confirmed on screen — uniform ShadCN spacing/typography throughout, one accent color, no template drift. The detector agrees (zero findings).

## Priority Issues

**[P0] No consent/purpose disclosure before biometric camera access**
Why it matters: the browser's native camera permission prompt fires with zero preceding explanation (`selfie-capture.tsx:50-60`), at the single highest-anxiety moment in the flow — and it's the most visually dominant element on the page when it appears. For biometric data this is also a plausible LGPD explicit-consent gap.
Fix: gate the `useEffect` behind an explicit "Start identity verification" action that shows a short privacy/purpose blurb (what's captured, why, retention) before requesting the camera.
Suggested command: `/impeccable onboard`

**[P1] Rejection flow under-discloses what must be redone**
Why it matters: copy says "Upload fresh documents and submit again" while the backend (and UI) also silently clears all 4 selfie clips — a user who already went through the anxiety of biometric capture once has no warning they must do it again.
Fix: rewrite the rejected-state copy to explicitly name what's being reset ("You'll need to re-upload your ID and re-record all 4 selfie clips"), and consider surfacing which specific item triggered rejection if the API returns it.
Suggested command: `/impeccable clarify`

**[P1] No section scaffolding across the four sub-flows**
Why it matters: confirmed on screen — ID upload and personal-info entry have no heading at all; only the selfie and address blocks do. Four distinct decisions blur into one scroll, driving the low Aesthetic/Minimalist and cognitive-load scores.
Fix: give each phase a real heading (or convert to a lightweight numbered stepper) and add a persistent "3 of 6 complete" checklist instead of the single static sentence at `page.tsx:273`.
Suggested command: `/impeccable layout`

**[P2] Inconsistent feedback and contrast polish**
Why it matters: document upload toasts success (`kyc-document-upload.tsx:43`) but selfie clip upload does not (`selfie-capture.tsx:41-45`) — asymmetric reinforcement across two mechanically similar actions. Separately, the locked/verified-state read-only fields render in noticeably lower-contrast muted gray on light gray, worth a dedicated WCAG check.
Fix: add matching `toast.success` per pose; audit disabled-input contrast in the verified state specifically.
Suggested command: `/impeccable polish`

**[P3] No preview or retake for uploaded documents/clips**
Why it matters: `DocumentList` shows only a label + date, no thumbnail, no delete/replace — a bad photo isn't caught until rejection days later.
Fix: thumbnail + delete/retake control per uploaded item.
Suggested command: `/impeccable harden`

## Persona Red Flags

**Jordan (First-Timer)**: Hits the camera permission dialog with zero warning — no prior "you'll do 4 short recordings" framing anywhere before they're already mid-flow. Confirmed the same on mobile, where the camera box is the first large visual element encountered.

**Sam (Accessibility-Dependent)**: The 4-dot pose indicator is `aria-hidden` (`selfie-capture.tsx:115`) with no ARIA label or live-region equivalent ("pose 2 of 4 captured") — screen reader users get zero step-progress signal. No non-video fallback path exists for the biometric modality at all.

**Riley (Stress-Tester)**: `record()` (`selfie-capture.tsx:72-89`) has no try/catch around `MediaRecorder` construction — an unsupported codec throws unhandled. Separately, personal-info form state (CPF/name/birth date/address) is local React state only (`page.tsx:211-212`) with no persistence, while uploaded documents *do* persist server-side — an inconsistent persistence model. A refresh mid-form silently loses typed personal info but keeps uploaded photos, which will confuse anyone who navigates away partway through.

## Minor Observations

- `zipNotFound` alert (`page.tsx:120-124`) isn't styled as `variant="destructive"` — easy to miss since it doesn't look like an error.
- Video preview capped at `max-w-xs` (`selfie-capture.tsx:106`) is small for judging framing, especially on mobile where it's already the dominant element.
- `SubmittedDetails` renders read-only values as disabled `<Input>` fields rather than plain text — technically fine, slightly unusual pattern, and the lower-contrast styling on these is the P2 contrast item above.

## Questions to Consider

- What would it feel like to see "Step 2 of 4: Prove you're really there" before the camera ever activates, instead of the camera just appearing?
- Does a rejection really need to wipe biometric clips when only the ID photo was flagged — or is that a backend constraint worth revisiting separately from this UI pass?
- If the six-item requirement were shown as a checklist from the very first screen, would the flow still feel aggressive, or just thorough?

# D4: The temperature warning starts at 70 °C, and the chart keeps its line at 60 °C

- **Date:** 2026-11-13
- **Status:** accepted; amends D2 and D3 (the temperature at which the gauge
  turns amber and the banner warns)
- **Decided by:** the user, on 2026-11-13: "raise it to 70". The agent proposed
  keeping the chart's line at 60 °C when it took the ruling. The banner's
  wording and the rounding to whole degrees are the agent's.

## Context

D2 turned the temperature gauge amber, and made the banner warn, from 60 °C.
D3's banner table kept that. 60 °C is the Raspberry Pi 3 B+'s soft limit: from
there its firmware runs the CPU at 1.2 GHz instead of 1.4 GHz.

Once Pi A was deployed, it idled at about 61 °C (2026-11-13). It sits in an
enclosure next to a warm network switch. On an open table it had measured
47 °C (2026-11-03). The user considers 61 °C normal for where it sits and
wants no fan work. At 60 °C, the public page would warn all the time, and a
warning that never clears hides the ones that matter.

## Decision

- **The warning starts at 70 °C.** From there the temperature gauge turns amber
  and the banner warns that the Pi "is running hot". This is `TempHot` in
  `dashboard/internal/view/view.go`. It's judged on the whole degree the page
  shows, so a Pi shown at 70 °C is always warned about, even at 69.5 °C.
- **The chart keeps its line at 60 °C**, labelled as where the Pi lowers its
  clock (`TempClock`). The line states a hardware fact, not an alarm.
- **The banner's text gives both temperatures:** the Pi lowers its clock from
  60 °C and slows further as it nears 85 °C, its hard limit (`TempLimit`,
  apart from the gauge's scale).
- **The gauge's scale stays 20–85 °C.**

So D2's banner rule and D3's Warning row read "at 70 °C or above" where they
say "at 60 °C or above", and D2's gauge reads "amber from 70 °C". D2's chart
line at 60 °C is unchanged.

## Consequences

- From 60 to 70 °C the page raises no alarm, even though the Pi runs at its
  lower clock. The chart's line still shows when it crossed 60 °C.
- A Pi that reaches 70 °C is well above Pi A's normal idle, so the warning
  points at something that changed: a heavier load, a blocked enclosure, or a
  warmer room.
- The threshold and the line are separate constants. Moving one doesn't move
  the other.

## Enforcement

`checks.x86_64-linux.dashboard` runs the view tests:

- `TestBanner`: 70.0 °C and 69.5 °C warn; 69.4 °C and 61.0 °C are "All good".
- `TestHotBannerText`: the warning names 60 °C and 85 °C.
- `TestGauges`: 70 °C and 69.5 °C are amber; 69.4 °C isn't.
- `TestChartsShape`: the temperature chart's line is labelled 60 °C and drawn
  at 60 °C.

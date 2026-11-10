---
name: Void Protocol
colors:
  surface: '#131313'
  surface-dim: '#131313'
  surface-bright: '#393939'
  surface-container-lowest: '#0e0e0e'
  surface-container-low: '#1b1b1b'
  surface-container: '#1f1f1f'
  surface-container-high: '#2a2a2a'
  surface-container-highest: '#353535'
  on-surface: '#e2e2e2'
  on-surface-variant: '#c4c7c8'
  inverse-surface: '#e2e2e2'
  inverse-on-surface: '#303030'
  outline: '#8e9192'
  outline-variant: '#444748'
  surface-tint: '#c6c6c7'
  primary: '#ffffff'
  on-primary: '#2f3131'
  primary-container: '#e2e2e2'
  on-primary-container: '#636565'
  inverse-primary: '#5d5f5f'
  secondary: '#ffb3ae'
  on-secondary: '#68000b'
  secondary-container: '#a90219'
  on-secondary-container: '#ffb3ad'
  tertiary: '#ffffff'
  on-tertiary: '#2f3131'
  tertiary-container: '#e2e2e2'
  on-tertiary-container: '#636565'
  error: '#ffb4ab'
  on-error: '#690005'
  error-container: '#93000a'
  on-error-container: '#ffdad6'
  primary-fixed: '#e2e2e2'
  primary-fixed-dim: '#c6c6c7'
  on-primary-fixed: '#1a1c1c'
  on-primary-fixed-variant: '#454747'
  secondary-fixed: '#ffdad7'
  secondary-fixed-dim: '#ffb3ae'
  on-secondary-fixed: '#410004'
  on-secondary-fixed-variant: '#930014'
  tertiary-fixed: '#e2e2e2'
  tertiary-fixed-dim: '#c6c6c7'
  on-tertiary-fixed: '#1a1c1c'
  on-tertiary-fixed-variant: '#454747'
  background: '#131313'
  on-background: '#e2e2e2'
  surface-variant: '#353535'
  void-black: '#000000'
  stark-white: '#FFFFFF'
  signal-red: '#FF4D4D'
  deep-gray: '#1A1A1A'
  muted-gray: '#888888'
typography:
  display:
    fontFamily: Inter
    fontSize: 48px
    fontWeight: '700'
    lineHeight: '1.1'
    letterSpacing: -0.02em
  headline-lg:
    fontFamily: Inter
    fontSize: 32px
    fontWeight: '600'
    lineHeight: '1.2'
    letterSpacing: -0.01em
  headline-lg-mobile:
    fontFamily: Inter
    fontSize: 24px
    fontWeight: '600'
    lineHeight: '1.2'
  headline-md:
    fontFamily: Inter
    fontSize: 24px
    fontWeight: '500'
    lineHeight: '1.3'
  body-lg:
    fontFamily: Inter
    fontSize: 18px
    fontWeight: '400'
    lineHeight: '1.6'
  body-md:
    fontFamily: Inter
    fontSize: 16px
    fontWeight: '400'
    lineHeight: '1.6'
  mono-code:
    fontFamily: JetBrains Mono
    fontSize: 14px
    fontWeight: '400'
    lineHeight: '1.5'
  mono-label:
    fontFamily: JetBrains Mono
    fontSize: 12px
    fontWeight: '500'
    lineHeight: '1.0'
    letterSpacing: 0.1em
  label-sm:
    fontFamily: Inter
    fontSize: 12px
    fontWeight: '500'
    lineHeight: '1.0'
spacing:
  unit: 4px
  gutter: 24px
  margin-mobile: 16px
  margin-desktop: 64px
  container-max: 1200px
---

## Brand & Style

This design system is built for a high-performance AI infrastructure environment. The brand personality is "Intelligent Precision"—it is focused, quiet, and authoritative. It draws heavily from **Minimalism** and **Technical Brutalism**, stripping away all non-essential decorative elements to allow data and agent workflows to take center stage.

The aesthetic follows the "Agent Community" ethos: a deep "void" black foundation that eliminates visual noise, high-contrast white typography for immediate legibility, and technical accents that evoke a terminal or command-line interface. The emotional response should be one of absolute clarity and computational power.

## Colors

The palette is strictly functional. **Void Black (#000000)** is the primary canvas, creating a sense of infinite depth. **Stark White (#FFFFFF)** is used for all primary content and interactive elements to ensure maximum contrast.

The accent color, **Signal Red (#FF4D4D)**, is reserved exclusively for "Status" indicators: errors, critical system alerts, or active agent states. Use **Deep Gray (#1A1A1A)** for subtle structural elements like borders and grid lines to maintain the hierarchy without breaking the "Void" aesthetic.

## Typography

The typography system relies on a strict hierarchy between **Inter** (for UI and narrative content) and **JetBrains Mono** (for technical data and labels). 

- **Inter** should be used for headlines and body text to ensure a modern, clean reading experience. Tighten letter spacing on larger displays to create a more "engineered" look.
- **JetBrains Mono** is the "Technical Voice." It must be used for any data output, status labels, timestamps, and code snippets. This font signals to the user that they are looking at raw intelligence or infrastructure details.

## Layout & Spacing

This design system utilizes a **Fixed Grid** philosophy within a "Void" space. Content is centered within a 1200px max-width container, surrounded by generous margins that emphasize focus.

- **The Grid:** Use a 12-column system for desktop with 24px gutters. For mobile, collapse to a single column with 16px side margins.
- **Micro-spacing:** Built on a 4px base unit. All padding and margins should be multiples of 4 (4, 8, 16, 24, 32, 48, 64).
- **Technical Accents:** Use 1px wide lines in Deep Gray (#1A1A1A) to define sections or create subtle background grids. Dot-matrix patterns can be used sparingly in the background to denote "active computation zones."

## Elevation & Depth

Depth is not communicated through shadows in this design system. Instead, it uses **Tonal Layers** and **Outlines**.

- **Level 0 (Surface):** Absolute Black (#000000).
- **Level 1 (Containers):** Defined by a 1px solid border of Deep Gray (#1A1A1A). No background fill change is required unless highlighting a specific interactive zone.
- **Interaction:** Hover states and active selections are indicated by a shift to a Stark White border or a very subtle dark gray fill (#0A0A0A). 
- **Focus:** No blurs or frosted glass. The system is sharp, flat, and definitive.

## Shapes

The shape language is **Sharp**. To maintain the high-performance technical aesthetic, all UI elements—including buttons, input fields, and cards—must have 0px corner radii.

The only exception to this rule is for specific circular status pips or specialized "agent avatars," but all structural UI components should remain rectangular and hard-edged.

## Components

- **Buttons:** Primary buttons are Stark White with Black text. Secondary buttons are Ghost style: 1px White border with White text and no fill. All buttons are rectangular (0px radius).
- **Inputs:** Simple 1px Deep Gray bottom border that turns White on focus. Use JetBrains Mono for the input text.
- **Chips / Tags:** Small, rectangular boxes with 1px borders. Use `mono-label` typography.
- **Status Indicators:** Use a small 8x8px square (not a circle) in Signal Red (#FF4D4D) for alerts or Stark White for neutral status.
- **Cards:** Defined by a 1px Deep Gray border. No shadows. Titles should be in `headline-md` and metadata in `mono-code`.
- **Data Tables:** No vertical lines. Use subtle horizontal 1px lines in Deep Gray. Header text must be `mono-label` in Muted Gray.
- **Technical Accents:** Use "Crosshair" symbols (+) at the corners of major containers to reinforce the "engineered" aesthetic.
import React from "react";

// === Base SVG Wrapper for strict Silicon Valley consistency ===
const BaseIcon = ({ children, size = 16, className = "" }: { children: React.ReactNode; size?: number; className?: string }) => (
  <svg
    xmlns="http://www.w3.org/2000/svg"
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.5"
    strokeLinecap="round"
    strokeLinejoin="round"
    className={`svg-icon ${className}`}
  >
    {children}
  </svg>
);

// === Navigation & Logistics Icons ===
export const IconTower = (p: any) => (
  <BaseIcon {...p}><path d="M12 2v20" /><path d="m8 6 8 0" /><path d="M5 10h14" /><path d="m3 14 18 0" /><path d="M2 18h20" /></BaseIcon>
);
export const IconFileText = (p: any) => (
  <BaseIcon {...p}><path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5L14.5 2z" /><polyline points="14 2 14 8 20 8" /><line x1="16" x2="8" y1="13" y2="13" /><line x1="16" x2="8" y1="17" y2="17" /><line x1="10" x2="8" y1="9" y2="9" /></BaseIcon>
);
export const IconGrid = (p: any) => (
  <BaseIcon {...p}><rect width="7" height="7" x="3" y="3" rx="1" /><rect width="7" height="7" x="14" y="3" rx="1" /><rect width="7" height="7" x="14" y="14" rx="1" /><rect width="7" height="7" x="3" y="14" rx="1" /></BaseIcon>
);
export const IconDatabase = (p: any) => (
  <BaseIcon {...p}><ellipse cx="12" cy="5" rx="9" ry="3" /><path d="M3 5V19A9 3 0 0 0 21 19V5" /><path d="M3 12A9 3 0 0 0 21 12" /></BaseIcon>
);
export const IconMapPin = (p: any) => (
  <BaseIcon {...p}><path d="M20 10c0 6-8 12-8 12s-8-6-8-12a8 8 0 0 1 16 0Z" /><circle cx="12" cy="10" r="3" /></BaseIcon>
);
export const IconTruck = (p: any) => (
  <BaseIcon {...p}><path d="M5 18H3c-.6 0-1-.4-1-1V7c0-.6.4-1 1-1h10c.6 0 1 .4 1 1v11" /><path d="M14 9h4l4 4v4c0 .6-.4 1-1 1h-2" /><circle cx="7" cy="18" r="2" /><circle cx="17" cy="18" r="2" /></BaseIcon>
);
export const IconAlert = (p: any) => (
  <BaseIcon {...p}><path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3Z" /><line x1="12" x2="12" y1="9" y2="13" /><line x1="12" x2="12.01" y1="17" y2="17" /></BaseIcon>
);
export const IconGitBranch = (p: any) => (
  <BaseIcon {...p}><line x1="6" x2="6" y1="3" y2="15" /><circle cx="18" cy="6" r="3" /><circle cx="6" cy="18" r="3" /><path d="M18 9a9 9 0 0 1-9 9" /></BaseIcon>
);
export const IconReceipt = (p: any) => (
  <BaseIcon {...p}><path d="M4 2v20l2-1 2 1 2-1 2 1 2-1 2 1 2-1 2 1V2l-2 1-2-1-2 1-2-1-2 1-2-1-2 1-2-1Z" /><path d="M16 8h-6a2 2 0 1 0 0 4h4a2 2 0 1 1 0 4H8" /><path d="M12 17V7" /></BaseIcon>
);
export const IconCreditCard = (p: any) => (
  <BaseIcon {...p}><rect width="20" height="14" x="2" y="5" rx="2" /><line x1="2" x2="22" y1="10" y2="10" /></BaseIcon>
);
export const IconShield = (p: any) => (
  <BaseIcon {...p}><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" /></BaseIcon>
);

// === Action & AI Icons ===
export const IconSparkles = (p: any) => (
  <BaseIcon {...p}><path d="m12 3-1.912 5.813a2 2 0 0 1-1.275 1.275L3 12l5.813 1.912a2 2 0 0 1 1.275 1.275L12 21l1.912-5.813a2 2 0 0 1 1.275-1.275L21 12l-5.813-1.912a2 2 0 0 1-1.275-1.275L12 3Z" /><path d="M5 3v4" /><path d="M19 17v4" /><path d="M3 5h4" /><path d="M17 19h4" /></BaseIcon>
);
export const IconZap = (p: any) => (
  <BaseIcon {...p}><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2" /></BaseIcon>
);
export const IconCheck = (p: any) => (
  <BaseIcon {...p}><polyline points="20 6 9 17 4 12" /></BaseIcon>
);
export const IconPlus = (p: any) => (
  <BaseIcon {...p}><line x1="12" x2="12" y1="5" y2="19" /><line x1="5" x2="19" y1="12" y2="12" /></BaseIcon>
);
export const IconX = (p: any) => (
  <BaseIcon {...p}><line x1="18" x2="6" y1="6" y2="18" /><line x1="6" x2="18" y1="6" y2="18" /></BaseIcon>
);
export const IconSave = (p: any) => (
  <BaseIcon {...p}><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z" /><polyline points="17 21 17 13 7 13 7 21" /><polyline points="7 3 7 8 15 8" /></BaseIcon>
);
export const IconTerminal = (p: any) => (
  <BaseIcon {...p}><polyline points="4 17 10 11 4 5" /><line x1="12" x2="20" y1="19" y2="19" /></BaseIcon>
);
export const IconSearch = (p: any) => (
  <BaseIcon {...p}><circle cx="11" cy="11" r="8" /><line x1="21" x2="16.65" y1="21" y2="16.65" /></BaseIcon>
);

// === Extended SVGs for Zero-Emoji UI ===
export const IconRobot = (p: any) => (
  <BaseIcon {...p}><rect width="18" height="14" x="3" y="7" rx="2" /><path d="M12 7V3" /><path d="M9 3h6" /><circle cx="9" cy="13" r="1.5" fill="currentColor" /><circle cx="15" cy="13" r="1.5" fill="currentColor" /><path d="M10 17h4" /></BaseIcon>
);
export const IconInfo = (p: any) => (
  <BaseIcon {...p}><circle cx="12" cy="12" r="10" /><path d="M12 16v-4" /><path d="M12 8h.01" /></BaseIcon>
);
export const IconMoney = (p: any) => (
  <BaseIcon {...p}><circle cx="12" cy="12" r="10" /><path d="M16 8h-6a2 2 0 1 0 0 4h4a2 2 0 1 1 0 4H8" /><path d="M12 18V6" /></BaseIcon>
);
export const IconWarning = (p: any) => (
  <BaseIcon {...p}><path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3Z" /><line x1="12" x2="12" y1="9" y2="13" /><line x1="12" x2="12.01" y1="17" y2="17" /></BaseIcon>
);
export const IconArrowRight = (p: any) => (
  <BaseIcon {...p}><path d="M5 12h14" /><path d="m12 5 7 7-7 7" /></BaseIcon>
);
export const IconCheckCircle = (p: any) => (
  <BaseIcon {...p}><circle cx="12" cy="12" r="10" /><path d="m9 12 2 2 4-4" /></BaseIcon>
);
export const IconXCircle = (p: any) => (
  <BaseIcon {...p}><circle cx="12" cy="12" r="10" /><path d="m15 9-6 6" /><path d="m9 9 6 6" /></BaseIcon>
);
export const IconBox = (p: any) => (
  <BaseIcon {...p}><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" /><polyline points="3.27 6.96 12 12.01 20.73 6.96" /><line x1="12" y1="22.08" x2="12" y2="12" /></BaseIcon>
);
export const IconDragHandle = (p: any) => (
  <BaseIcon {...p}><circle cx="9" cy="5" r="1" fill="currentColor"/><circle cx="9" cy="12" r="1" fill="currentColor"/><circle cx="9" cy="19" r="1" fill="currentColor"/><circle cx="15" cy="5" r="1" fill="currentColor"/><circle cx="15" cy="12" r="1" fill="currentColor"/><circle cx="15" cy="19" r="1" fill="currentColor"/></BaseIcon>
);

import { expect } from "./fixture.js";

export const expectPassagePlacement = async (page) => {
  await expect(page.locator('.passage-tools')).toBeVisible();
  await expect.poll(() => page.evaluate(() => {
    const bar = document.querySelector('.passage-tools').getBoundingClientRect();
    if (matchMedia('(pointer: coarse)').matches) return Math.abs(innerHeight - bar.bottom - 16) < 2;
    const rect = getSelection().getRangeAt(0).getBoundingClientRect();
    return bar.left >= 0 && bar.right <= innerWidth && bar.top >= 0 && bar.bottom <= innerHeight &&
      (bar.bottom <= rect.top || bar.top >= rect.bottom || bar.right <= rect.left || bar.left >= rect.right);
  })).toBe(true);
};

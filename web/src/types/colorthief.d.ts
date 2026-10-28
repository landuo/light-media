declare module 'colorthief/dist/color-thief.mjs' {
  export default class ColorThief {
    getColor(image: HTMLImageElement, quality?: number): number[];
  }
}

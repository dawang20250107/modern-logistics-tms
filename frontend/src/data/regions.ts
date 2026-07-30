// 全国省/市/区三级行政区（来源 china-division，已内置到仓库）。
// 用于三级级联选址与"城市库"补全；地图选址在配置高德 key 后接入。
import pca from "./regions.json";

type PCA = Record<string, Record<string, string[]>>;
const DATA = pca as PCA;

export const PROVINCES: string[] = Object.keys(DATA);

export function citiesOf(province: string): string[] {
  return province && DATA[province] ? Object.keys(DATA[province]) : [];
}

export function districtsOf(province: string, city: string): string[] {
  return province && city && DATA[province]?.[city] ? DATA[province][city] : [];
}

// 扁平化的全量城市名（去重），用于城市组合框补全"城市库不全"。
export const ALL_CITIES: string[] = (() => {
  const set = new Set<string>();
  for (const prov of PROVINCES) {
    for (const city of citiesOf(prov)) {
      // 直辖市的"市辖区"等无意义层级用省名代替
      set.add(city === "市辖区" || city === "县" ? prov.replace(/(省|市|自治区|特别行政区)$/, "") : city.replace(/(市|地区|自治州|盟)$/, ""));
    }
  }
  return [...set];
})();

// 反查：给定城市名（市级）猜其所属省，供级联联动。
export function provinceOfCity(cityShort: string): string {
  for (const prov of PROVINCES) {
    for (const city of citiesOf(prov)) {
      if (city.replace(/(市|地区|自治州|盟)$/, "") === cityShort) return prov;
    }
  }
  return "";
}

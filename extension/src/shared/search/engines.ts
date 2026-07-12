import type { SearchEngine } from "../profile/types";

export type SearchEngineIconKind =
  | "baidu"
  | "google"
  | "bing"
  | "sogou"
  | "so360"
  | "duckduckgo"
  | "yandex"
  | "yahoo"
  | "naver"
  | "github"
  | "bilibili"
  | "zhihu"
  | "wechat"
  | "wikipedia"
  | "youtube"
  | "npm"
  | "mdn"
  | "stackoverflow"
  | "initial";

export type SearchEngineIconView = {
  kind: SearchEngineIconKind;
  label: string;
  title: string;
};

export const defaultSearchEngines: SearchEngine[] = [
  {
    id: "baidu",
    title: "百度",
    template: "https://www.baidu.com/s?wd={query}"
  },
  {
    id: "google",
    title: "Google",
    template: "https://www.google.com/search?q={query}"
  },
  {
    id: "bing",
    title: "Bing",
    template: "https://www.bing.com/search?q={query}"
  },
  {
    id: "sogou",
    title: "搜狗",
    template: "https://www.sogou.com/web?query={query}"
  },
  {
    id: "so360",
    title: "360 搜索",
    template: "https://www.so.com/s?q={query}"
  },
  {
    id: "duckduckgo",
    title: "DuckDuckGo",
    template: "https://duckduckgo.com/?q={query}"
  },
  {
    id: "yandex",
    title: "Yandex",
    template: "https://yandex.com/search/?text={query}"
  },
  {
    id: "yahoo",
    title: "Yahoo",
    template: "https://search.yahoo.com/search?p={query}"
  },
  {
    id: "naver",
    title: "Naver",
    template: "https://search.naver.com/search.naver?query={query}"
  },
  {
    id: "github",
    title: "GitHub",
    template: "https://github.com/search?q={query}"
  },
  {
    id: "bilibili",
    title: "哔哩哔哩",
    template: "https://search.bilibili.com/all?keyword={query}"
  },
  {
    id: "zhihu",
    title: "知乎",
    template: "https://www.zhihu.com/search?q={query}"
  },
  {
    id: "wechat",
    title: "微信文章",
    template: "https://weixin.sogou.com/weixin?type=2&query={query}"
  },
  {
    id: "wikipedia",
    title: "Wikipedia",
    template: "https://en.wikipedia.org/w/index.php?search={query}"
  },
  {
    id: "youtube",
    title: "YouTube",
    template: "https://www.youtube.com/results?search_query={query}"
  },
  {
    id: "npm",
    title: "npm",
    template: "https://www.npmjs.com/search?q={query}"
  },
  {
    id: "mdn",
    title: "MDN",
    template: "https://developer.mozilla.org/search?q={query}"
  },
  {
    id: "stackoverflow",
    title: "Stack Overflow",
    template: "https://stackoverflow.com/search?q={query}"
  }
];

const searchEngineIconKinds: Record<string, SearchEngineIconKind> = {
  baidu: "baidu",
  google: "google",
  bing: "bing",
  sogou: "sogou",
  so360: "so360",
  duckduckgo: "duckduckgo",
  yandex: "yandex",
  yahoo: "yahoo",
  naver: "naver",
  github: "github",
  bilibili: "bilibili",
  zhihu: "zhihu",
  wechat: "wechat",
  wikipedia: "wikipedia",
  youtube: "youtube",
  npm: "npm",
  mdn: "mdn",
  stackoverflow: "stackoverflow"
};

function getInitialLabel(title: string) {
  return Array.from(title.trim())[0]?.toUpperCase() ?? "?";
}

export function mergeSearchEngines(existing: SearchEngine[] | undefined) {
  const map = new Map<string, SearchEngine>();
  for (const engine of defaultSearchEngines) {
    map.set(engine.id, engine);
  }
  for (const engine of existing ?? []) {
    map.set(engine.id, engine);
  }

  return defaultSearchEngines.map((engine) => map.get(engine.id)!);
}

export function buildSearchUrl(engine: SearchEngine, query: string) {
  return engine.template.replace("{query}", encodeURIComponent(query.trim()));
}

export function getSearchEngineIcon(engine: Pick<SearchEngine, "id" | "title">): SearchEngineIconView {
  const kind = searchEngineIconKinds[engine.id] ?? "initial";
  return {
    kind,
    label: kind === "initial" ? getInitialLabel(engine.title) : engine.title,
    title: engine.title
  };
}

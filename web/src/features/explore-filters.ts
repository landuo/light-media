export type ExploreSource = 'themoviedb' | 'douban' | 'bangumi' | 'anilist';
export const exploreSources: { id: ExploreSource; name: string; color: string }[] = [
  { id: 'themoviedb', name: 'TheMovieDb', color: 'primary' },
  { id: 'douban', name: '豆瓣', color: 'success' },
  { id: 'bangumi', name: 'Bangumi', color: 'info' },
  { id: 'anilist', name: 'AniList', color: 'warning' },
];

export interface ExploreFilterState {
  type: string;
  sort_by: string;
  with_genres: string;
  with_original_language: string;
  with_keywords: string;
  with_watch_providers: string;
  vote_average: number;
  vote_count: number | string;
  release_date: string;
  sort: string;
  category: string;
  zone: string;
  year: string;
  cat: string;
  format: string;
  genre: string;
  season: string;
  season_year: string;
  status: string;
  country: string;
}

export function createExploreFilters(source: ExploreSource): ExploreFilterState {
  return {
    type: source === 'bangumi' ? '2' : source === 'anilist' ? 'ANIME' : 'movies',
    sort_by: 'popularity.desc',
    with_genres: '',
    with_original_language: '',
    with_keywords: '',
    with_watch_providers: '',
    vote_average: 0,
    vote_count: 10,
    release_date: '',
    sort: source === 'douban' ? 'U' : source === 'anilist' ? 'POPULARITY_DESC' : 'rank',
    category: '',
    zone: '',
    year: '',
    cat: '',
    format: '',
    genre: '',
    season: '',
    season_year: '',
    status: '',
    country: '',
  };
}

export const mediaTypes = { movies: '电影', tvs: '电视剧' };
export const tmdbMovieSort = {
  'popularity.desc': '热度降序',
  'popularity.asc': '热度升序',
  'release_date.desc': '上映日期降序',
  'release_date.asc': '上映日期升序',
  'vote_average.desc': '评分降序',
  'vote_average.asc': '评分升序',
};
export const tmdbTVSort = {
  'popularity.desc': '热度降序',
  'popularity.asc': '热度升序',
  'first_air_date.desc': '首播日期降序',
  'first_air_date.asc': '首播日期升序',
  'vote_average.desc': '评分降序',
  'vote_average.asc': '评分升序',
};
export const tmdbMovieGenres = {
  '28': '动作',
  '12': '冒险',
  '16': '动画',
  '35': '喜剧',
  '80': '犯罪',
  '99': '纪录片',
  '18': '剧情',
  '10751': '家庭',
  '14': '奇幻',
  '36': '历史',
  '27': '恐怖',
  '10402': '音乐',
  '9648': '悬疑',
  '10749': '爱情',
  '878': '科幻',
  '10770': '电视电影',
  '53': '惊悚',
  '10752': '战争',
  '37': '西部',
};
export const tmdbTVGenres = {
  '10759': '动作冒险',
  '16': '动画',
  '35': '喜剧',
  '80': '犯罪',
  '99': '纪录片',
  '18': '剧情',
  '10751': '家庭',
  '10762': '儿童',
  '9648': '悬疑',
  '10763': '新闻',
  '10764': '真人秀',
  '10765': '科幻奇幻',
  '10766': '肥皂剧',
  '10767': '戏剧',
  '10768': '战争政治',
  '37': '西部',
};
export const tmdbLanguages = {
  zh: '中文',
  en: '英语',
  ja: '日语',
  ko: '韩语',
  fr: '法语',
  de: '德语',
  es: '西班牙语',
  it: '意大利语',
  ru: '俄语',
  pt: '葡萄牙语',
  ar: '阿拉伯语',
  hi: '印地语',
  th: '泰语',
};
export const doubanSort = { U: '综合排序', R: '首播时间', T: '近期热度', S: '高分优先' };
const labels = (values: string[]) => Object.fromEntries(values.map((value) => [value, value]));
export const doubanGenres = labels([
  '喜剧',
  '爱情',
  '动作',
  '科幻',
  '动画',
  '悬疑',
  '犯罪',
  '惊悚',
  '冒险',
  '音乐',
  '历史',
  '奇幻',
  '恐怖',
  '战争',
  '传记',
  '歌舞',
  '武侠',
  '情色',
  '灾难',
  '西部',
  '纪录片',
  '短片',
]);
export const doubanZones = labels([
  '华语',
  '欧美',
  '韩国',
  '日本',
  '中国大陆',
  '美国',
  '中国香港',
  '中国台湾',
  '英国',
  '法国',
  '德国',
  '意大利',
  '西班牙',
  '印度',
  '泰国',
  '俄罗斯',
  '加拿大',
  '澳大利亚',
  '爱尔兰',
  '瑞典',
  '巴西',
  '丹麦',
]);
export function exploreYears(source: ExploreSource, currentYear = new Date().getFullYear()) {
  const years =
    source === 'douban'
      ? labels(['2020年代', '2010年代', '2000年代', '90年代', '80年代', '70年代', '60年代'])
      : ({} as Record<string, string>);
  for (let i = 0; i < (source === 'douban' ? 6 : 10); i++) {
    years[String(currentYear - i)] = String(currentYear - i);
  }
  return years;
}
export const bangumiCategories = { '0': '其他', '1': 'TV', '2': 'OVA', '3': 'Movie', '5': 'WEB' };
export const bangumiSort = { rank: '排名', date: '日期' };

export const anilistSort = {
  POPULARITY_DESC: '热门优先',
  TRENDING_DESC: '趋势优先',
  SCORE_DESC: '评分优先',
  START_DATE_DESC: '最新开播',
};
export const anilistFormats = {
  TV: 'TV',
  TV_SHORT: '短篇 TV',
  MOVIE: '剧场版',
  OVA: 'OVA',
  ONA: 'ONA',
  SPECIAL: '特别篇',
  MUSIC: '音乐',
};
export const anilistGenres = {
  Action: '动作',
  Adventure: '冒险',
  Comedy: '喜剧',
  Drama: '剧情',
  Fantasy: '奇幻',
  Horror: '恐怖',
  'Mahou Shoujo': '魔法少女',
  Mecha: '机甲',
  Music: '音乐',
  Mystery: '悬疑',
  Psychological: '心理',
  Romance: '爱情',
  'Sci-Fi': '科幻',
  'Slice of Life': '日常',
  Sports: '运动',
  Supernatural: '超自然',
  Thriller: '惊悚',
};
export const anilistSeasons = { WINTER: '冬季', SPRING: '春季', SUMMER: '夏季', FALL: '秋季' };
export const anilistStatuses = {
  RELEASING: '连载中',
  FINISHED: '已完结',
  NOT_YET_RELEASED: '未播出',
};
export const anilistCountries = { JP: '日本', CN: '中国大陆', KR: '韩国', TW: '中国台湾' };
export function anilistYears(currentYear = new Date().getFullYear()): [string, string][] {
  return Array.from({ length: 15 }, (_, i) => [String(currentYear - i), String(currentYear - i)]);
}

// Match the original source's defaults and retain selections shared by both media types.
export function normalizeExploreFilters(source: ExploreSource, filters: ExploreFilterState) {
  filters.type ||= source === 'bangumi' ? '2' : source === 'anilist' ? 'ANIME' : 'movies';
  if (source === 'themoviedb') {
    const sort = filters.type === 'movies' ? tmdbMovieSort : tmdbTVSort;
    const genres = filters.type === 'movies' ? tmdbMovieGenres : tmdbTVGenres;
    if (!(filters.sort_by in sort)) filters.sort_by = 'popularity.desc';
    if (!(filters.with_genres in genres)) filters.with_genres = '';
  } else if (source === 'douban') {
    filters.sort ||= 'U';
  } else if (source === 'anilist') {
    filters.sort ||= 'POPULARITY_DESC';
  }
}

export function exploreQuery(source: ExploreSource, filters: ExploreFilterState, page: number) {
  const params = new URLSearchParams({ source, type: filters.type, page: String(page) });
  if (source === 'themoviedb') {
    for (const key of [
      'sort_by',
      'with_genres',
      'with_original_language',
      'with_keywords',
      'with_watch_providers',
      'vote_average',
      'vote_count',
      'release_date',
    ] as const) {
      params.set(key, String(filters[key]));
    }
  } else if (source === 'douban') {
    params.set('sort', filters.sort);
    params.set('tags', [filters.category, filters.zone, filters.year].filter(Boolean).join(','));
  } else if (source === 'bangumi') {
    params.set('sort', filters.sort);
    if (filters.cat !== '') params.set('cat', filters.cat);
    if (filters.year !== '') params.set('year', filters.year);
  } else {
    params.set('sort', filters.sort);
    params.set('count', '20');
    for (const key of ['format', 'genre', 'season', 'season_year', 'status', 'country'] as const) {
      if (filters[key]) params.set(key, filters[key]);
    }
  }
  return params.toString();
}

export function normalizeExploreOrder(order: readonly string[] = []): ExploreSource[] {
  const known = exploreSources.map((source) => source.id);
  return [
    ...new Set([
      ...order.filter((id): id is ExploreSource => known.includes(id as ExploreSource)),
      ...known,
    ]),
  ];
}

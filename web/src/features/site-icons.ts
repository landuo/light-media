import icon0 from '../assets/sites/ubits.club.ico';
import icon1 from '../assets/sites/hdhome.org.ico';
import icon2 from '../assets/sites/hhanclub.net.ico';
import icon3 from '../assets/sites/m-team.cc.png';
import icon4 from '../assets/sites/hdfans.org.ico';
import icon5 from '../assets/sites/xloli.cc.ico';
import icon6 from '../assets/sites/pandapt.net.ico';
import icon7 from '../assets/sites/btschool.club.ico';
import icon8 from '../assets/sites/pttime.org.ico';
import icon9 from '../assets/sites/1ptba.com.ico';
import icon10 from '../assets/sites/xingyungept.org.ico';
import icon11 from '../assets/sites/52pt.site.ico';
import icon12 from '../assets/sites/carpt.net.ico';
import icon13 from '../assets/sites/musopia.vip.ico';
import icon14 from '../assets/sites/rousi.pro.ico';
import icon15 from '../assets/sites/cspt.top.ico';
import icon16 from '../assets/sites/raingfh.top.png';
import defaultIcon from '../assets/sites/default.webp';
export { defaultIcon };
const icons: Record<string, string> = {
  'ubits.club': icon0,
  'hdhome.org': icon1,
  'hhanclub.net': icon2,
  'm-team.cc': icon3,
  'hdfans.org': icon4,
  'xloli.cc': icon5,
  'pandapt.net': icon6,
  'btschool.club': icon7,
  'pttime.org': icon8,
  '1ptba.com': icon9,
  'xingyungept.org': icon10,
  '52pt.site': icon11,
  'carpt.net': icon12,
  'musopia.vip': icon13,
  'rousi.pro': icon14,
  'cspt.top': icon15,
  'raingfh.top': icon16,
};
export function siteIconURL(value: string): string {
  try {
    const u = new URL(value);
    if (u.protocol !== 'https:' || u.username || u.password) return defaultIcon;
    const host = u.hostname.toLowerCase();
    for (const [domain, icon] of Object.entries(icons))
      if (host === domain || host.endsWith('.' + domain)) return icon;
    return new URL('/favicon.ico', u.origin).href;
  } catch {
    return defaultIcon;
  }
}

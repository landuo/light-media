# Plugin icon sources

These images are unmodified copies used to match MoviePilot's plugin cards.
They are served locally; opening the plugin list does not request these assets
from GitHub or any other third-party host.

- `Bookstack_A.png`, `Dsfinder_A.png`, `Duplicati_A.png`, `FeiShu_A.png`,
  `IYUU.png`, `Ombi_A.png`, `Time_machine_B.png`, `brush-flow.png`, `clean.png`,
  `clean_a.png`, `cloudflare.jpg`, `hosts.png`, `refresh2.png`, `seed.png`,
  `signin.png`, `statistic.png`, `teamwork.png`, `words.png`, `world.png`:
  [MoviePilot-Plugins icons](https://github.com/landuo/MoviePilot-Plugins/tree/5a5e8e3ac4c5cd75ac66cf3ef5ef04144ca90c32/icons),
  repository snapshot `5a5e8e3ac4c5cd75ac66cf3ef5ef04144ca90c32`.
  The repository distributes these assets under its GPL-3.0 license
  (included as `LICENSE-GPL-3.0.txt`). Upstream artwork and trademarks remain
  the property of their respective owners.
- `MergeMessageNotify_121.png`:
  [MoviePilot-Plugins-Third](https://github.com/hotlcc/MoviePilot-Plugins-Third/blob/main/icons/MergeMessageNotify_121.png),
  retrieved 2026-10-23, SHA-256
  `0d50ed7606d84308e3475a4b296e38c0db131bd7648706cd8a835a56866b53b5`.
  Repository license: GPL-3.0.
- `plugin.png`: MoviePilot's fallback icon, copied from
  [MoviePilot-Frontend](https://github.com/jxxghp/MoviePilot-Frontend/blob/2829e3b08214348f05ff2f5beae644882eccad58/src/assets/images/logos/plugin.png).
  MIT license, copyright (c) 2023 jxxghp
  (included as `LICENSE-MoviePilot-Frontend.txt`).

`uninstall.png` is absent from the original icon collection and the running
MoviePilot static endpoint (HTTP 404, checked 2026-10-23); it intentionally uses
MoviePilot's `plugin.png` fallback.

`web/src/features/plugin-icon-colors.json` stores the dominant color of each
bundled icon. It was generated with the same Color Thief 2.6.0 quantization as
MoviePilot (`getColor(image, 20)`, five palette colors). This lets cards use
the original image-derived colors without a new browser dependency or a
runtime external image request. Images were decoded as PNG/JPEG and checked
for nonzero dimensions before inclusion.

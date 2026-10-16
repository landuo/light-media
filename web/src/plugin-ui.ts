import { createVuetify } from 'vuetify';
import { aliases } from 'vuetify/iconsets/mdi';
import { h } from 'vue';
import { Icon, addCollection } from '@iconify/vue/offline';
import mdiIcons from './assets/material-design-icons.json';
import './assets/plugin-utilities.css';

// Original forms supply icon names, rather than SVG path data. Bundle the
// original MDI shapes locally so every declared field icon works offline.
addCollection(mdiIcons);
addCollection(mdiIcons, 'mdi-');

// Match the original project's component version, theme and form defaults.
// Import component styles as needed, without applying Vuetify's global reset
// to the application's existing pages.
export const pluginUI = createVuetify({
  icons: {
    defaultSet: 'iconify',
    aliases: {
      ...aliases,
      success: 'mdi-check-circle-outline',
      info: 'mdi-information-outline',
      warning: 'mdi-alert-outline',
      error: 'mdi-alert-circle-outline',
    },
    sets: { iconify: { component: (props: any) => h(Icon, props) } },
  },
  theme: {
    defaultTheme: 'light',
    themes: {
      light: {
        dark: false,
        colors: {
          primary: '#9155FD',
          secondary: '#8A8D93',
          success: '#56CA00',
          info: '#16B1FF',
          warning: '#FFB400',
          error: '#FF4C51',
          background: '#F4F5FA',
          surface: '#FFFFFF',
          'on-surface': '#3A3541',
          'on-primary': '#FFFFFF',
        },
        variables: {
          'hover-opacity': 0.04,
          'focus-opacity': 0.1,
          'selected-opacity': 0.12,
          'border-color': '#3A3541',
          'border-opacity': 0.12,
        },
      },
    },
  },
  defaults: {
    VBtn: { color: 'primary', elevation: 0 },
    VCard: { elevation: 0, rounded: 'lg' },
    VDialog: { elevation: 0, rounded: 'lg', transition: false },
    VTextField: { variant: 'outlined', color: 'primary', hideDetails: 'auto' },
    VTextarea: { variant: 'outlined', color: 'primary', hideDetails: 'auto' },
    VSelect: { variant: 'outlined', color: 'primary', hideDetails: 'auto' },
    VSwitch: { color: 'primary', hideDetails: 'auto' },
  },
});

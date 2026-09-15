import { createVuetify } from 'vuetify'
import { defineNuxtPlugin } from '#app'

export default defineNuxtPlugin((nuxtApp) => {
  const vuetify = createVuetify({
    ssr: false,
    theme: {
      defaultTheme: 'dark',
      themes: {
        dark: {
          dark: true,
          colors: {
            primary: '#6366f1',
            'primary-darken-1': '#4f46e5',
            secondary: '#a855f7',
            background: '#090d16',
            surface: '#111827',
            'surface-variant': '#1e293b',
            success: '#10b981',
            warning: '#f59e0b',
            error: '#f43f5e',
            info: '#06b6d4',
          }
        },
        light: {
          dark: false,
          colors: {
            primary: '#4f46e5',
            'primary-darken-1': '#4338ca',
            secondary: '#9333ea',
            background: '#f8fafc',
            surface: '#ffffff',
            'surface-variant': '#f1f5f9',
            success: '#10b981',
            warning: '#f59e0b',
            error: '#e11d48',
            info: '#0284c7',
          }
        }
      }
    },
    defaults: {
      VCard: {
        elevation: 2,
        rounded: 'lg',
      },
      VBtn: {
        rounded: 'md',
        elevation: 0,
      },
      VTextField: {
        variant: 'outlined',
        density: 'comfortable',
      },
      VSelect: {
        variant: 'outlined',
        density: 'comfortable',
      }
    }
  })

  nuxtApp.vueApp.use(vuetify)
})

import { defineNuxtConfig } from 'nuxt/config'
import vuetify, { transformAssetUrls } from 'vite-plugin-vuetify'
import { resolve } from 'path'

export default defineNuxtConfig({
  ssr: false,
  compatibilityDate: '2025-01-01',
  app: {
    baseURL: '/admin/',
    head: {
      title: 'GoAssistant Web Admin',
      meta: [
        { charset: 'utf-8' },
        { name: 'viewport', content: 'width=device-width, initial-scale=1' }
      ]
    }
  },
  css: [
    'vuetify/styles',
    '@mdi/font/css/materialdesignicons.css'
  ],
  build: {
    transpile: ['vuetify']
  },
  modules: [
    (_options, nuxt) => {
      nuxt.hooks.hook('vite:extendConfig', (config) => {
        // @ts-expect-error vite plugin
        config.plugins.push(vuetify({ autoImport: true }))
      })
    }
  ],
  vite: {
    vue: {
      template: {
        transformAssetUrls
      }
    }
  },
  nitro: {
    output: {
      publicDir: resolve(__dirname, '../internal/webadmin/ui')
    }
  }
})

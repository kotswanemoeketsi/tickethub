/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { VitePWA } from 'vite-plugin-pwa'

// During development the Go API runs on :8080. Proxying keeps the browser on a single
// origin, so the HttpOnly refresh-token cookie works exactly as it does in production.
const api = process.env.VITE_API_PROXY ?? 'http://localhost:8080'

const appName = process.env.VITE_APP_NAME ?? 'GotATicket'
const day = 24 * 60 * 60

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    VitePWA({
      // A new version waits until the user chooses to reload (see UpdatePrompt), so nobody
      // loses a half-finished checkout to a background update.
      registerType: 'prompt',
      injectRegister: false,
      includeAssets: ['favicon.ico', 'icon.svg', 'icons/apple-touch-icon.png'],
      manifest: {
        id: '/',
        name: appName,
        short_name: appName,
        description: 'Concerts, comedy, festivals and more across South Africa. Buy tickets in seconds and keep them on your phone.',
        lang: 'en-ZA',
        start_url: '/',
        scope: '/',
        display: 'standalone',
        orientation: 'any',
        background_color: '#f6f5fa',
        theme_color: '#15123a',
        categories: ['entertainment', 'lifestyle', 'events'],
        icons: [
          { src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
          { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
          { src: '/icons/icon-maskable-192.png', sizes: '192x192', type: 'image/png', purpose: 'maskable' },
          { src: '/icons/icon-maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
          { src: '/icon.svg', sizes: 'any', type: 'image/svg+xml', purpose: 'any' },
        ],
        screenshots: [
          { src: '/screenshots/home-wide.png', sizes: '1280x800', type: 'image/png', form_factor: 'wide', label: 'Find events across South Africa' },
          { src: '/screenshots/home-narrow.png', sizes: '390x844', type: 'image/png', form_factor: 'narrow', label: 'Events near you' },
          { src: '/screenshots/tickets-narrow.png', sizes: '390x844', type: 'image/png', form_factor: 'narrow', label: 'Your tickets, ready at the door' },
        ],
        shortcuts: [
          { name: 'My tickets', url: '/account/tickets', icons: [{ src: '/icons/icon-192.png', sizes: '192x192' }] },
          { name: 'Find events', url: '/events', icons: [{ src: '/icons/icon-192.png', sizes: '192x192' }] },
          { name: 'Scan tickets', url: '/scan', icons: [{ src: '/icons/icon-192.png', sizes: '192x192' }] },
        ],
      },
      workbox: {
        // The app shell: every page, script, style and the Latin fonts, so the app opens
        // without a connection. Source maps and other font subsets are fetched on demand.
        globPatterns: ['**/*.{js,css,html,ico,png,svg}', 'assets/*-latin-*.woff2'],
        globIgnores: ['**/*.map', 'screenshots/**'],
        maximumFileSizeToCacheInBytes: 3 * 1024 * 1024,
        navigateFallback: '/index.html',
        // These are served by the API, not the app, so they must never get the app shell.
        navigateFallbackDenylist: [/^\/api\//, /^\/uploads\//, /^\/sandbox\//],
        cleanupOutdatedCaches: true,
        // Only public, shared data is cached by the service worker. Anything tied to a
        // signed-in user (orders, tickets, the admin area) always goes to the network;
        // tickets have their own on-device copy, which is cleared when the user signs out.
        runtimeCaching: [
          {
            urlPattern: ({ url, request }) => request.method === 'GET' && url.pathname === '/api/v1/public/places',
            handler: 'StaleWhileRevalidate',
            options: { cacheName: 'places', expiration: { maxEntries: 12, maxAgeSeconds: 30 * day } },
          },
          {
            urlPattern: ({ url, request }) => request.method === 'GET' && url.pathname.startsWith('/api/v1/public/'),
            handler: 'NetworkFirst',
            options: {
              cacheName: 'public-api',
              networkTimeoutSeconds: 4,
              expiration: { maxEntries: 80, maxAgeSeconds: day },
              cacheableResponse: { statuses: [200] },
            },
          },
          {
            urlPattern: ({ url, request }) => request.method === 'GET' && url.pathname.startsWith('/uploads/'),
            handler: 'CacheFirst',
            options: {
              cacheName: 'images',
              expiration: { maxEntries: 120, maxAgeSeconds: 30 * day, purgeOnQuotaError: true },
              cacheableResponse: { statuses: [0, 200] },
            },
          },
        ],
      },
      devOptions: { enabled: false },
    }),
  ],
  server: {
    port: 5173,
    host: true,
    proxy: {
      '/api': api,
      '/uploads': api,
      '/sandbox': api,
    },
  },
  build: {
    sourcemap: true,
    chunkSizeWarningLimit: 900,
  },
  test: {
    environment: 'node',
  },
})

import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ApiError, isNetworkError } from './lib/api'
import { AuthProvider } from './lib/auth'
import { listenForInstallPrompt } from './lib/pwa'
import { ToastProvider } from './components/toast'
import { App } from './App'
import './index.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      refetchOnWindowFocus: false,
      // Client errors (not found, forbidden, validation) will not succeed on retry, and
      // neither will anything while the device is offline.
      retry: (failures, error) =>
        !(error instanceof ApiError && error.status < 500) && !(isNetworkError(error) && !navigator.onLine) && failures < 2,
    },
  },
})

listenForInstallPrompt()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <ToastProvider>
          <AuthProvider>
            <App />
          </AuthProvider>
        </ToastProvider>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)

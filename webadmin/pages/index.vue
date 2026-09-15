<template>
  <div class="webadmin-container">
    <!-- Login Dialog (Telegram 2FA OTP) -->
    <v-dialog v-model="loginRequired" persistent max-width="480" backdrop="blur">
      <v-card class="pa-4 pa-sm-6" rounded="xl" color="surface">
        <div class="text-center mb-4">
          <v-avatar color="primary" size="64" class="mb-3 elevation-4">
            <v-icon size="36" color="white">mdi-shield-lock-outline</v-icon>
          </v-avatar>
          <h2 class="text-h5 font-weight-bold text-white">GoAssistant Web Admin</h2>
          <p class="text-body-2 text-medium-emphasis mt-1">
            Masuk dengan verifikasi OTP Telegram Anda yang terdaftar sebagai Administrator.
          </p>
        </div>

        <v-alert
          v-if="loginError"
          type="error"
          variant="tonal"
          closable
          class="mb-4"
          @click:close="loginError = ''"
        >
          {{ loginError }}
        </v-alert>

        <v-alert
          v-if="loginSuccessMsg"
          type="success"
          variant="tonal"
          class="mb-4"
        >
          {{ loginSuccessMsg }}
        </v-alert>

        <!-- Step 1: Telegram User ID -->
        <div v-if="loginStep === 1">
          <v-text-field
            v-model="telegramIdInput"
            label="Telegram User ID Admin"
            placeholder="Contoh: 123456789"
            type="number"
            prepend-inner-icon="mdi-account-cog-outline"
            hint="Dapatkan Telegram User ID dari bot @userinfobot"
            persistent-hint
            autofocus
            class="mb-4"
            @keyup.enter="handleRequestOTP"
          />

          <v-btn
            color="primary"
            block
            size="large"
            rounded="lg"
            :loading="requestingOTP"
            @click="handleRequestOTP"
          >
            <v-icon start>mdi-send-check-outline</v-icon>
            Kirim Kode OTP ke Telegram
          </v-btn>
        </div>

        <!-- Step 2: 6-Digit OTP Input -->
        <div v-else-if="loginStep === 2">
          <div class="text-center mb-2">
            <span class="text-caption text-medium-emphasis">
              Masukkan 6 digit kode yang dikirimkan oleh Bot Telegram Anda:
            </span>
          </div>

          <v-otp-input
            v-model="otpInput"
            length="6"
            type="number"
            class="mb-4"
            :disabled="verifyingOTP"
            @finish="handleVerifyOTP"
          />

          <v-btn
            color="primary"
            block
            size="large"
            rounded="lg"
            :loading="verifyingOTP"
            @click="handleVerifyOTP"
          >
            <v-icon start>mdi-lock-open-check-outline</v-icon>
            Verifikasi & Masuk
          </v-btn>

          <div class="d-flex justify-space-between align-center mt-4">
            <v-btn
              variant="text"
              size="small"
              prepend-icon="mdi-arrow-left"
              @click="loginStep = 1"
            >
              Ganti ID
            </v-btn>
            <span class="text-caption text-medium-emphasis">
              {{ resendCountdown > 0 ? `Kirim ulang dalam ${resendCountdown}s` : '' }}
            </span>
          </div>
        </div>
      </v-card>
    </v-dialog>

    <!-- Main Navigation Bar -->
    <v-app-bar color="surface" elevation="1" border="b">
      <v-container fluid class="d-flex align-center py-0">
        <div class="d-flex align-center me-4">
          <v-avatar color="primary" size="36" class="me-2">
            <v-icon size="20" color="white">mdi-robot-outline</v-icon>
          </v-avatar>
          <div>
            <div class="font-weight-bold text-subtitle-1 line-height-1">GoAssistant</div>
            <div class="text-caption text-medium-emphasis">Web Admin Control Plane</div>
          </div>
        </div>

        <v-tabs v-model="activeTab" color="primary" density="comfortable" class="ms-sm-6">
          <v-tab value="activities">
            <v-icon start>mdi-chart-timeline-variant</v-icon>
            <span class="d-none d-sm-inline">Aktivitas & Log</span>
          </v-tab>
          <v-tab value="chat">
            <v-icon start>mdi-chat-processing-outline</v-icon>
            <span class="d-none d-sm-inline">Chat Assistant</span>
          </v-tab>
          <v-tab value="system">
            <v-icon start>mdi-server-network</v-icon>
            <span class="d-none d-sm-inline">Status & Port</span>
          </v-tab>
        </v-tabs>

        <v-spacer />

        <div class="d-flex align-center ga-2">
          <v-chip
            v-if="currentAdminId"
            color="success"
            variant="tonal"
            size="small"
            prepend-icon="mdi-account-check-outline"
          >
            Admin ({{ currentAdminId }})
          </v-chip>

          <v-btn
            icon="mdi-logout"
            variant="text"
            color="error"
            size="small"
            title="Keluar dari Web Admin"
            @click="handleLogout"
          />
        </div>
      </v-container>
    </v-app-bar>

    <!-- Main Workspace Content -->
    <v-main class="bg-background">
      <v-container fluid class="pa-4 pa-sm-6" style="max-width: 1440px;">
        <v-window v-model="activeTab">
          <!-- TAB 1: Aktivitas & Log -->
          <v-window-item value="activities">
            <!-- Stats Summary Cards -->
            <v-row class="mb-4">
              <v-col cols="12" sm="6" md="3">
                <v-card color="surface" class="pa-4">
                  <div class="d-flex align-center justify-space-between">
                    <div>
                      <div class="text-caption text-medium-emphasis text-uppercase font-weight-bold">
                        Total Permintaan
                      </div>
                      <div class="text-h4 font-weight-bold mt-1 text-white">
                        {{ totalLogs.toLocaleString() }}
                      </div>
                    </div>
                    <v-avatar color="primary" variant="tonal" size="48" rounded="lg">
                      <v-icon size="28">mdi-chart-bar</v-icon>
                    </v-avatar>
                  </div>
                </v-card>
              </v-col>

              <v-col cols="12" sm="6" md="3">
                <v-card color="surface" class="pa-4">
                  <div class="d-flex align-center justify-space-between">
                    <div>
                      <div class="text-caption text-medium-emphasis text-uppercase font-weight-bold">
                        Success Rate
                      </div>
                      <div class="text-h4 font-weight-bold mt-1 text-success">
                        {{ successRate }}%
                      </div>
                    </div>
                    <v-avatar color="success" variant="tonal" size="48" rounded="lg">
                      <v-icon size="28">mdi-check-decagram</v-icon>
                    </v-avatar>
                  </div>
                </v-card>
              </v-col>

              <v-col cols="12" sm="6" md="3">
                <v-card color="surface" class="pa-4">
                  <div class="d-flex align-center justify-space-between">
                    <div>
                      <div class="text-caption text-medium-emphasis text-uppercase font-weight-bold">
                        Tokens Terpakai
                      </div>
                      <div class="text-h4 font-weight-bold mt-1 text-warning">
                        {{ statsTokensUsed.toLocaleString() }}
                      </div>
                    </div>
                    <v-avatar color="warning" variant="tonal" size="48" rounded="lg">
                      <v-icon size="28">mdi-lightning-bolt</v-icon>
                    </v-avatar>
                  </div>
                </v-card>
              </v-col>

              <v-col cols="12" sm="6" md="3">
                <v-card color="surface" class="pa-4">
                  <div class="d-flex align-center justify-space-between">
                    <div>
                      <div class="text-caption text-medium-emphasis text-uppercase font-weight-bold">
                        Tokens Saved (RTK)
                      </div>
                      <div class="text-h4 font-weight-bold mt-1 text-secondary">
                        {{ statsTokensSaved.toLocaleString() }}
                      </div>
                    </div>
                    <v-avatar color="secondary" variant="tonal" size="48" rounded="lg">
                      <v-icon size="28">mdi-leaf</v-icon>
                    </v-avatar>
                  </div>
                </v-card>
              </v-col>
            </v-row>

            <!-- Filters & Toolbar -->
            <v-card color="surface" class="pa-4 mb-4">
              <v-row dense align="center">
                <v-col cols="12" md="4">
                  <v-text-field
                    v-model="searchQuery"
                    prepend-inner-icon="mdi-magnify"
                    label="Cari prompt, pengguna, respon, model..."
                    hide-details
                    clearable
                    @keyup.enter="fetchActivities(1)"
                  />
                </v-col>

                <v-col cols="6" sm="4" md="2">
                  <v-select
                    v-model="filterChannel"
                    :items="channelOptions"
                    label="Channel"
                    hide-details
                    @update:model-value="fetchActivities(1)"
                  />
                </v-col>

                <v-col cols="6" sm="4" md="2">
                  <v-select
                    v-model="filterStatus"
                    :items="statusOptions"
                    label="Status"
                    hide-details
                    @update:model-value="fetchActivities(1)"
                  />
                </v-col>

                <v-col cols="12" sm="4" md="4" class="text-sm-end mt-2 mt-sm-0">
                  <v-btn
                    prepend-icon="mdi-refresh"
                    variant="tonal"
                    color="primary"
                    :loading="loadingActivities"
                    @click="fetchActivities(1)"
                  >
                    Segarkan
                  </v-btn>
                </v-col>
              </v-row>
            </v-card>

            <!-- Activities Table -->
            <v-card color="surface" class="mb-4">
              <v-table hover>
                <thead>
                  <tr>
                    <th class="text-uppercase font-weight-bold text-medium-emphasis">Waktu</th>
                    <th class="text-uppercase font-weight-bold text-medium-emphasis">Channel</th>
                    <th class="text-uppercase font-weight-bold text-medium-emphasis">Pengguna</th>
                    <th class="text-uppercase font-weight-bold text-medium-emphasis">Model / Provider</th>
                    <th class="text-uppercase font-weight-bold text-medium-emphasis">Tokens</th>
                    <th class="text-uppercase font-weight-bold text-medium-emphasis">Latensi</th>
                    <th class="text-uppercase font-weight-bold text-medium-emphasis">Biaya</th>
                    <th class="text-uppercase font-weight-bold text-medium-emphasis">Status</th>
                    <th class="text-uppercase font-weight-bold text-medium-emphasis text-center">Aksi</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-if="loadingActivities">
                    <td colspan="9" class="text-center py-6 text-medium-emphasis">
                      <v-progress-circular indeterminate color="primary" class="me-2" size="20" />
                      Memuat riwayat aktivitas...
                    </td>
                  </tr>
                  <tr v-else-if="activities.length === 0">
                    <td colspan="9" class="text-center py-6 text-medium-emphasis">
                      Tidak ada aktivitas yang sesuai dengan filter.
                    </td>
                  </tr>
                  <tr v-for="item in activities" :key="item.id">
                    <td class="text-no-wrap font-monospace text-caption">
                      {{ formatTime(item.timestamp) }}
                    </td>
                    <td>
                      <v-chip size="x-small" color="primary" variant="tonal" class="text-capitalize">
                        {{ item.channel_type || 'web' }}
                      </v-chip>
                    </td>
                    <td class="font-weight-medium">
                      {{ item.user_name || 'Admin' }}
                    </td>
                    <td class="text-no-wrap">
                      <span class="text-body-2">{{ item.model || '-' }}</span>
                      <span class="text-caption text-medium-emphasis ms-1">({{ item.provider || '-' }})</span>
                    </td>
                    <td class="font-monospace text-caption">
                      {{ (item.total_tokens || 0).toLocaleString() }}
                    </td>
                    <td>{{ item.latency_ms || 0 }}ms</td>
                    <td class="font-monospace">${{ (item.cost_usd || 0).toFixed(4) }}</td>
                    <td>
                      <v-chip
                        size="x-small"
                        :color="item.status === 'success' ? 'success' : 'error'"
                        variant="tonal"
                        class="text-uppercase"
                      >
                        {{ item.status }}
                      </v-chip>
                    </td>
                    <td class="text-center">
                      <v-btn
                        size="x-small"
                        variant="tonal"
                        color="primary"
                        prepend-icon="mdi-eye-outline"
                        @click="openLogDetail(item)"
                      >
                        Detail
                      </v-btn>
                    </td>
                  </tr>
                </tbody>
              </v-table>

              <!-- Pagination Footer -->
              <v-divider />
              <div class="d-flex flex-wrap justify-space-between align-center pa-4">
                <span class="text-caption text-medium-emphasis">
                  Menampilkan {{ ((currentPage - 1) * pageSize) + 1 }} -
                  {{ Math.min(currentPage * pageSize, totalLogs) }} dari {{ totalLogs }} data
                </span>
                <v-pagination
                  v-model="currentPage"
                  :length="totalPages"
                  :total-visible="5"
                  density="comfortable"
                  @update:model-value="fetchActivities"
                />
              </div>
            </v-card>
          </v-window-item>

          <!-- TAB 2: AI Chat Assistant -->
          <v-window-item value="chat">
            <v-card color="surface" height="calc(100vh - 130px)" class="d-flex flex-column">
              <!-- Chat Header -->
              <div class="pa-4 border-b d-flex justify-space-between align-center">
                <div class="d-flex align-center">
                  <v-avatar color="primary" variant="tonal" size="40" class="me-3">
                    <v-icon size="24">mdi-forum-outline</v-icon>
                  </v-avatar>
                  <div>
                    <div class="font-weight-bold">GoAssistant AI Web Chat</div>
                    <div class="text-caption text-medium-emphasis">
                      {{ chatSubtitle }}
                    </div>
                  </div>
                </div>

                <v-btn
                  variant="tonal"
                  color="warning"
                  size="small"
                  prepend-icon="mdi-delete-sweep-outline"
                  @click="handleResetChat"
                >
                  Reset Chat
                </v-btn>
              </div>

              <!-- Chat Message Scroll Area -->
              <div ref="chatScrollRef" class="flex-grow-1 overflow-y-auto pa-4 pa-sm-6 d-flex flex-column ga-4">
                <div
                  v-for="(msg, idx) in chatMessages"
                  :key="idx"
                  class="d-flex ga-3"
                  :class="msg.role === 'user' ? 'justify-end' : 'justify-start'"
                >
                  <!-- Avatar for Assistant -->
                  <v-avatar
                    v-if="msg.role === 'assistant'"
                    color="success"
                    size="36"
                    class="flex-shrink-0 mt-1"
                  >
                    <v-icon size="20" color="white">mdi-robot</v-icon>
                  </v-avatar>

                  <!-- Bubble -->
                  <div style="max-width: 80%;">
                    <!-- Thinking Details if present -->
                    <v-expansion-panels v-if="msg.thinking" class="mb-2" variant="inset">
                      <v-expansion-panel
                        title="💭 Proses Berpikir Model AI"
                        elevation="0"
                        bg-color="surface-variant"
                      >
                        <v-expansion-panel-text>
                          <pre class="thinking-pre text-caption font-monospace pa-2">{{ msg.thinking }}</pre>
                        </v-expansion-panel-text>
                      </v-expansion-panel>
                    </v-expansion-panels>

                    <v-card
                      :color="msg.role === 'user' ? 'primary' : 'surface-variant'"
                      class="pa-3 pa-sm-4 bubble-card"
                      elevation="1"
                    >
                      <div class="markdown-body" v-html="renderMarkdown(msg.content)" />
                    </v-card>
                  </div>

                  <!-- Avatar for User -->
                  <v-avatar
                    v-if="msg.role === 'user'"
                    color="primary"
                    size="36"
                    class="flex-shrink-0 mt-1"
                  >
                    <v-icon size="20" color="white">mdi-account</v-icon>
                  </v-avatar>
                </div>
              </div>

              <!-- Chat Input Box -->
              <div class="pa-4 border-t bg-surface">
                <div class="d-flex align-end ga-2">
                  <v-textarea
                    v-model="chatInput"
                    placeholder="Ketik pesan untuk asisten (Enter untuk kirim, Shift+Enter untuk baris baru)..."
                    rows="1"
                    auto-grow
                    max-rows="5"
                    hide-details
                    variant="outlined"
                    density="comfortable"
                    class="flex-grow-1"
                    @keydown.enter.exact.prevent="sendChatMessage"
                  />
                  <v-btn
                    color="primary"
                    icon="mdi-send"
                    size="large"
                    :loading="chatStreaming"
                    :disabled="!chatInput.trim()"
                    @click="sendChatMessage"
                  />
                </div>
              </div>
            </v-card>
          </v-window-item>

          <!-- TAB 3: Status & Port Server -->
          <v-window-item value="system">
            <v-row>
              <!-- System Runtime Card -->
              <v-col cols="12" md="6">
                <v-card color="surface" class="pa-6">
                  <div class="d-flex align-center mb-4">
                    <v-avatar color="primary" variant="tonal" size="44" class="me-3">
                      <v-icon size="26">mdi-server-network</v-icon>
                    </v-avatar>
                    <div>
                      <h3 class="text-h6 font-weight-bold">Status Runtime Server</h3>
                      <div class="text-caption text-medium-emphasis">Informasi kesehatan daemon GoAssistant</div>
                    </div>
                  </div>

                  <v-divider class="mb-4" />

                  <div class="d-flex flex-column ga-3">
                    <div class="d-flex justify-space-between align-center">
                      <span class="text-medium-emphasis">Uptime GoAssistant:</span>
                      <span class="font-weight-bold">{{ sysStats.uptime_formatted || '-' }}</span>
                    </div>
                    <v-divider />
                    <div class="d-flex justify-space-between align-center">
                      <span class="text-medium-emphasis">Memory Allocated:</span>
                      <span class="font-weight-bold font-monospace">
                        {{ (sysStats.memory_alloc_mb || 0).toFixed(1) }} MB (Sys: {{ (sysStats.memory_sys_mb || 0).toFixed(1) }} MB)
                      </span>
                    </div>
                    <v-divider />
                    <div class="d-flex justify-space-between align-center">
                      <span class="text-medium-emphasis">Active Goroutines:</span>
                      <span class="font-weight-bold font-monospace">{{ sysStats.goroutines || '-' }}</span>
                    </div>
                    <v-divider />
                    <div class="d-flex justify-space-between align-center">
                      <span class="text-medium-emphasis">Go Version:</span>
                      <span class="font-weight-bold font-monospace">{{ sysStats.go_version || '-' }}</span>
                    </div>
                    <v-divider />
                    <div class="d-flex justify-space-between align-center">
                      <span class="text-medium-emphasis">Waktu Server:</span>
                      <span class="font-weight-bold">{{ sysStats.server_time || '-' }}</span>
                    </div>
                  </div>
                </v-card>
              </v-col>

              <!-- Web Admin Port Configuration Card -->
              <v-col cols="12" md="6">
                <v-card color="surface" class="pa-6">
                  <div class="d-flex align-center mb-4">
                    <v-avatar color="secondary" variant="tonal" size="44" class="me-3">
                      <v-icon size="26">mdi-cog-sync-outline</v-icon>
                    </v-avatar>
                    <div>
                      <h3 class="text-h6 font-weight-bold">Konfigurasi Port Web Admin</h3>
                      <div class="text-caption text-medium-emphasis">Pengaturan port dinamis (Tersinkronisasi Telegram)</div>
                    </div>
                  </div>

                  <v-alert type="info" variant="tonal" class="mb-4" density="comfortable">
                    Port Web Admin dapat diubah langsung di sini atau sewaktu-waktu melalui Telegram bot dengan perintah:
                    <code class="text-white font-weight-bold ms-1">/setwebport &lt;port&gt;</code>
                  </v-alert>

                  <v-card color="surface-variant" class="pa-4 mb-4" rounded="lg">
                    <div class="text-caption text-medium-emphasis text-uppercase">Port Aktif Saat Ini:</div>
                    <div class="text-h3 font-weight-bold text-primary my-1">
                      {{ currentWebPort }}
                    </div>
                    <div class="text-caption text-medium-emphasis font-monospace">
                      {{ currentHostUrl }}
                    </div>
                  </v-card>

                  <v-text-field
                    v-model="newPortInput"
                    label="Ubah ke Port Baru (1024 - 65535)"
                    type="number"
                    min="1024"
                    max="65535"
                    prepend-inner-icon="mdi-numeric"
                    placeholder="Contoh: 8088 atau 12111"
                    class="mb-3"
                  />

                  <v-btn
                    color="primary"
                    block
                    size="large"
                    prepend-icon="mdi-content-save-cog"
                    :loading="updatingPort"
                    @click="handleSavePort"
                  >
                    Simpan & Pindahkan Port Sekarang
                  </v-btn>
                </v-card>
              </v-col>
            </v-row>
          </v-window-item>
        </v-window>
      </v-container>
    </v-main>

    <!-- Detail Activity Modal Dialog -->
    <v-dialog v-model="detailDialog" max-width="800">
      <v-card color="surface" rounded="xl" v-if="selectedLog">
        <v-card-title class="d-flex justify-space-between align-center pa-4 pa-sm-6 border-b">
          <span class="font-weight-bold">Rincian Log Aktivitas</span>
          <v-btn icon="mdi-close" variant="text" size="small" @click="detailDialog = false" />
        </v-card-title>

        <v-card-text class="pa-4 pa-sm-6 overflow-y-auto" style="max-height: 70vh;">
          <v-row dense class="mb-2">
            <v-col cols="12" sm="6">
              <div class="text-caption text-medium-emphasis font-weight-bold text-uppercase">ID Transaksi</div>
              <div class="font-monospace text-caption bg-surface-variant pa-2 rounded mt-1">{{ selectedLog.id }}</div>
            </v-col>
            <v-col cols="12" sm="6">
              <div class="text-caption text-medium-emphasis font-weight-bold text-uppercase">Waktu Eksekusi</div>
              <div class="bg-surface-variant pa-2 rounded mt-1 text-body-2">{{ selectedLog.timestamp }}</div>
            </v-col>
          </v-row>

          <v-row dense class="mb-4">
            <v-col cols="4">
              <div class="text-caption text-medium-emphasis font-weight-bold text-uppercase">Channel</div>
              <div class="mt-1"><v-chip size="small" color="primary">{{ selectedLog.channel_type }}</v-chip></div>
            </v-col>
            <v-col cols="4">
              <div class="text-caption text-medium-emphasis font-weight-bold text-uppercase">Pengguna</div>
              <div class="mt-1 text-body-2 font-weight-medium">{{ selectedLog.user_name }} ({{ selectedLog.user_id }})</div>
            </v-col>
            <v-col cols="4">
              <div class="text-caption text-medium-emphasis font-weight-bold text-uppercase">Model AI</div>
              <div class="mt-1 text-body-2">{{ selectedLog.model }} ({{ selectedLog.provider }})</div>
            </v-col>
          </v-row>

          <div class="mb-3">
            <div class="text-caption text-medium-emphasis font-weight-bold text-uppercase mb-1">Prompt Pengguna</div>
            <v-card color="surface-variant" class="pa-3 text-body-2" elevation="0">
              <pre class="white-space-pre-wrap">{{ selectedLog.client_request || '(Kosong)' }}</pre>
            </v-card>
          </div>

          <div class="mb-3">
            <div class="text-caption text-medium-emphasis font-weight-bold text-uppercase mb-1">Respon Asisten AI</div>
            <v-card color="surface-variant" class="pa-3 text-body-2" elevation="0" max-height="300" style="overflow-y: auto;">
              <div class="markdown-body" v-html="renderMarkdown(selectedLog.provider_response || '')" />
            </v-card>
          </div>

          <div class="mb-3">
            <div class="text-caption text-medium-emphasis font-weight-bold text-uppercase mb-1">Tools Dipanggil</div>
            <div class="font-monospace text-caption bg-surface-variant pa-2 rounded">
              {{ selectedLog.tools_called && selectedLog.tools_called !== '[]' ? selectedLog.tools_called : 'Tidak ada' }}
            </div>
          </div>

          <div v-if="selectedLog.error_message" class="mb-3">
            <div class="text-caption text-error font-weight-bold text-uppercase mb-1">Error Message</div>
            <v-alert type="error" variant="tonal" density="compact">{{ selectedLog.error_message }}</v-alert>
          </div>
        </v-card-text>
      </v-card>
    </v-dialog>

    <!-- Global Snackbar -->
    <v-snackbar v-model="snackbar.show" :color="snackbar.color" :timeout="3500">
      {{ snackbar.text }}
      <template #actions>
        <v-btn variant="text" @click="snackbar.show = false">Tutup</v-btn>
      </template>
    </v-snackbar>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, nextTick } from 'vue'
import MarkdownIt from 'markdown-it'

const md = new MarkdownIt({
  html: false,
  linkify: true,
  breaks: true,
})

// State variables
const loginRequired = ref(true)
const loginStep = ref(1)
const telegramIdInput = ref('')
const otpInput = ref('')
const requestingOTP = ref(false)
const verifyingOTP = ref(false)
const loginError = ref('')
const loginSuccessMsg = ref('')
const resendCountdown = ref(0)
const currentAdminId = ref(null)

const activeTab = ref('activities')

// Activity State
const activities = ref([])
const totalLogs = ref(0)
const currentPage = ref(1)
const pageSize = ref(20)
const loadingActivities = ref(false)
const searchQuery = ref('')
const filterChannel = ref('all')
const filterStatus = ref('all')
const detailDialog = ref(false)
const selectedLog = ref(null)

const channelOptions = [
  { title: 'Semua Channel', value: 'all' },
  { title: 'Telegram', value: 'telegram' },
  { title: 'WhatsApp', value: 'whatsapp' },
  { title: 'Web Admin', value: 'web' }
]

const statusOptions = [
  { title: 'Semua Status', value: 'all' },
  { title: 'Success', value: 'success' },
  { title: 'Error', value: 'error' }
]

// Chat State
const chatMessages = ref([
  {
    role: 'assistant',
    content: 'Halo Administrator! Saya adalah asisten AI GoAssistant. Ada yang bisa saya bantu terkait monitoring, analisis log, atau konfigurasi sistem?'
  }
])
const chatInput = ref('')
const chatStreaming = ref(false)
const chatSubtitle = ref('Terkoneksi langsung ke AI Orchestrator')
const chatScrollRef = ref(null)

// System & Port State
const sysStats = ref({})
const currentWebPort = ref(12111)
const currentHostUrl = ref('')
const newPortInput = ref('')
const updatingPort = ref(false)

// Snackbar
const snackbar = ref({
  show: false,
  text: '',
  color: 'info'
})

function showToast(text, color = 'info') {
  snackbar.value = { show: true, text, color }
}

// Computed stats
const successRate = computed(() => {
  if (activities.value.length === 0) return 100
  const successCount = activities.value.filter(a => a.status === 'success').length
  return Math.round((successCount / activities.value.length) * 100)
})

const statsTokensUsed = computed(() => {
  return activities.value.reduce((acc, a) => acc + (a.total_tokens || 0), 0)
})

const statsTokensSaved = computed(() => {
  return activities.value.reduce((acc, a) => acc + (a.tokens_saved || 0), 0)
})

const totalPages = computed(() => {
  return Math.max(1, Math.ceil(totalLogs.value / pageSize.value))
})

// Authentication Handlers
async function checkAuth() {
  try {
    const res = await fetch('/api/auth/me')
    if (res.ok) {
      const data = await res.json()
      currentAdminId.value = data.telegram_id
      loginRequired.value = false
      fetchActivities(1)
      fetchSystemStats()
      fetchChatHistory()
    } else {
      loginRequired.value = true
    }
  } catch (e) {
    loginRequired.value = true
  }
}

async function handleRequestOTP() {
  const idVal = parseInt(telegramIdInput.value, 10)
  if (!idVal) {
    loginError.value = 'Silakan masukkan Telegram User ID Anda'
    return
  }

  loginError.value = ''
  requestingOTP.value = true

  try {
    const res = await fetch('/api/auth/request-otp', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ telegram_id: idVal })
    })
    const data = await res.json()
    if (res.ok) {
      loginStep.value = 2
      loginSuccessMsg.value = 'Kode OTP telah dikirimkan ke akun Telegram Anda!'
      startCountdown(15)
    } else {
      loginError.value = data.error || 'Gagal mengirimkan kode OTP'
    }
  } catch (err) {
    loginError.value = 'Koneksi error: ' + err.message
  } finally {
    requestingOTP.value = false
  }
}

function startCountdown(sec) {
  resendCountdown.value = sec
  const timer = setInterval(() => {
    resendCountdown.value--
    if (resendCountdown.value <= 0) {
      clearInterval(timer)
    }
  }, 1000)
}

async function handleVerifyOTP() {
  if (!otpInput.value || otpInput.value.length !== 6) {
    loginError.value = 'Masukkan 6 digit kode OTP'
    return
  }

  loginError.value = ''
  verifyingOTP.value = true

  try {
    const res = await fetch('/api/auth/verify-otp', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        telegram_id: parseInt(telegramIdInput.value, 10),
        otp: otpInput.value
      })
    })
    const data = await res.json()
    if (res.ok) {
      loginRequired.value = false
      currentAdminId.value = data.telegram_id
      showToast('Login berhasil! Selamat datang di Web Admin.', 'success')
      fetchActivities(1)
      fetchSystemStats()
      fetchChatHistory()
    } else {
      loginError.value = data.error || 'Kode OTP tidak valid'
    }
  } catch (err) {
    loginError.value = 'Verifikasi error: ' + err.message
  } finally {
    verifyingOTP.value = false
  }
}

async function handleLogout() {
  if (!confirm('Apakah Anda yakin ingin keluar dari Web Admin?')) return
  try {
    await fetch('/api/auth/logout', { method: 'POST' })
  } catch (e) {}
  window.location.reload()
}

// Activity Handlers
async function fetchActivities(page = 1) {
  currentPage.value = page
  loadingActivities.value = true

  try {
    const params = new URLSearchParams({
      page: currentPage.value.toString(),
      limit: pageSize.value.toString(),
      channel: filterChannel.value,
      status: filterStatus.value,
      search: searchQuery.value.trim()
    })

    const res = await fetch('/api/activities?' + params.toString())
    if (!res.ok) throw new Error('Gagal memuat log aktivitas')
    const data = await res.json()

    activities.value = data.items || []
    totalLogs.value = data.total || 0
  } catch (err) {
    showToast('Gagal memuat aktivitas: ' + err.message, 'error')
  } finally {
    loadingActivities.value = false
  }
}

function openLogDetail(logItem) {
  selectedLog.value = logItem
  detailDialog.value = true
}

function formatTime(timestamp) {
  if (!timestamp) return '-'
  return timestamp.replace('T', ' ').substring(0, 19)
}

function renderMarkdown(text) {
  if (!text) return ''
  return md.render(text)
}

// Chat Handlers
async function sendChatMessage() {
  const text = chatInput.value.trim()
  if (!text || chatStreaming.value) return

  // Append user message
  chatMessages.value.push({ role: 'user', content: text })
  chatInput.value = ''

  // Append empty assistant message placeholder
  const assistantMsgIndex = chatMessages.value.length
  chatMessages.value.push({ role: 'assistant', content: '', thinking: '' })

  chatStreaming.value = true
  chatSubtitle.value = 'Sedang memproses respon AI...'
  scrollChatBottom()

  try {
    const response = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message: text })
    })

    if (!response.ok) throw new Error('Server error: ' + response.statusText)

    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''

    while (true) {
      const { value, done } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop()

      let currentEvent = 'message'
      for (const line of lines) {
        if (line.startsWith('event: ')) {
          currentEvent = line.substring(7).trim()
        } else if (line.startsWith('data: ')) {
          const dataStr = line.substring(6).trim()
          if (!dataStr) continue

          try {
            const parsed = JSON.parse(dataStr)
            if (currentEvent === 'chunk' && parsed.text) {
              chatMessages.value[assistantMsgIndex].content += parsed.text
            } else if (currentEvent === 'thinking' && parsed.text) {
              chatMessages.value[assistantMsgIndex].thinking += parsed.text
            } else if (currentEvent === 'progress' && parsed.status) {
              chatSubtitle.value = '⏳ ' + parsed.status
            } else if (currentEvent === 'error') {
              chatMessages.value[assistantMsgIndex].content += `\n\n> ⚠️ **Error:** ${parsed.error}`
            }
          } catch (e) {}
        }
      }
      scrollChatBottom()
    }
  } catch (err) {
    chatMessages.value[assistantMsgIndex].content += `\n\n> ⚠️ **Koneksi terputus:** ${err.message}`
  } finally {
    chatStreaming.value = false
    chatSubtitle.value = 'Terkoneksi langsung ke AI Orchestrator'
    scrollChatBottom()
  }
}

function scrollChatBottom() {
  nextTick(() => {
    if (chatScrollRef.value) {
      chatScrollRef.value.scrollTop = chatScrollRef.value.scrollHeight
    }
  })
}

async function fetchChatHistory() {
  try {
    const res = await fetch('/api/chat/history')
    if (!res.ok) return
    const data = await res.json()
    if (data.messages && data.messages.length > 0) {
      chatMessages.value = data.messages.map(m => ({
        role: m.role,
        content: m.content,
        thinking: ''
      }))
      scrollChatBottom()
    }
  } catch (e) {}
}

async function handleResetChat() {
  if (!confirm('Bersihkan riwayat percakapan web chat?')) return
  try {
    await fetch('/api/chat/clear', { method: 'POST' })
    chatMessages.value = [
      {
        role: 'assistant',
        content: 'Riwayat obrolan berhasil dibersihkan. Silakan mulai topik percakapan baru!',
        thinking: ''
      }
    ]
    showToast('Riwayat chat berhasil direset', 'success')
  } catch (e) {
    showToast('Gagal mereset chat', 'error')
  }
}

// System & Port Handlers
async function fetchSystemStats() {
  try {
    const res = await fetch('/api/system/stats')
    if (!res.ok) return
    const data = await res.json()
    sysStats.value = data
    currentWebPort.value = data.current_web_port || 12111
    currentHostUrl.value = window.location.protocol + '//' + window.location.hostname + ':' + currentWebPort.value + '/admin'
  } catch (e) {}
}

async function handleSavePort() {
  const p = parseInt(newPortInput.value, 10)
  if (!p || p < 1024 || p > 65535) {
    alert('Nomor port harus berupa angka antara 1024 sampai 65535.')
    return
  }

  if (!confirm(`Konfirmasi: Pindahkan port Web Admin ke ${p}? Halaman akan berpindah ke port baru.`)) {
    return
  }

  updatingPort.value = true
  try {
    const res = await fetch('/api/system/port', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ port: p })
    })
    const data = await res.json()
    if (res.ok) {
      showToast(`Port berhasil diubah ke ${p}. Mengalihkan...`, 'success')
      setTimeout(() => {
        window.location.href = window.location.protocol + '//' + window.location.hostname + ':' + p + '/admin'
      }, 1500)
    } else {
      alert('Gagal mengubah port: ' + (data.error || 'Unknown error'))
      updatingPort.value = false
    }
  } catch (e) {
    showToast(`Server sedang berpindah ke port ${p}...`, 'info')
    setTimeout(() => {
      window.location.href = window.location.protocol + '//' + window.location.hostname + ':' + p + '/admin'
    }, 2000)
  }
}

onMounted(() => {
  checkAuth()
})
</script>

<style scoped>
.webadmin-container {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

.white-space-pre-wrap {
  white-space: pre-wrap;
  word-break: break-word;
  font-family: inherit;
}

.thinking-pre {
  background: rgba(0, 0, 0, 0.2);
  border-radius: 6px;
  color: #fde68a;
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 200px;
  overflow-y: auto;
}

.bubble-card {
  border-radius: 16px !important;
  word-break: break-word;
}

/* Markdown typography inside chat bubble */
:deep(.markdown-body) {
  font-size: 0.925rem;
  line-height: 1.6;
}

:deep(.markdown-body p) {
  margin-bottom: 0.5rem;
}

:deep(.markdown-body p:last-child) {
  margin-bottom: 0;
}

:deep(.markdown-body pre) {
  background: #0b1329 !important;
  border-radius: 8px;
  padding: 10px 14px;
  margin: 8px 0;
  overflow-x: auto;
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.825rem;
}

:deep(.markdown-body code) {
  background: rgba(255, 255, 255, 0.1);
  padding: 2px 6px;
  border-radius: 4px;
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.85em;
}
</style>

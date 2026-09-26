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
            <v-btn
              variant="text"
              size="small"
              :disabled="resendCountdown > 0"
              @click="handleRequestOTP"
            >
              {{ resendCountdown > 0 ? `Kirim ulang (${resendCountdown}s)` : 'Kirim Ulang OTP' }}
            </v-btn>
          </div>
        </div>
      </v-card>
    </v-dialog>

    <!-- Top App Bar Navigation -->
    <v-app-bar flat color="surface" density="comfortable" class="border-b">
      <v-container fluid class="d-flex align-center justify-space-between px-4">
        <div class="d-flex align-center">
          <v-avatar color="primary" size="36" class="me-3 elevation-2">
            <v-icon size="20" color="white">mdi-lightning-bolt</v-icon>
          </v-avatar>
          <div>
            <div class="font-weight-bold text-subtitle-1 leading-tight text-high-emphasis">
              GoAssistant <span class="text-primary font-weight-light">Admin</span>
            </div>
            <div class="text-caption text-medium-emphasis">Control Plane & Analytics</div>
          </div>
        </div>

        <!-- Navigation Tabs -->
        <v-tabs v-model="activeTab" density="compact" color="primary" class="d-none d-sm-flex">
          <v-tab value="activities" prepend-icon="mdi-chart-timeline-variant-shimmer">
            Aktivitas & Log
          </v-tab>
          <v-tab value="chat" prepend-icon="mdi-robot">
            AI Web Chat
          </v-tab>
          <v-tab value="system" prepend-icon="mdi-server-network">
            Status & Jaringan
          </v-tab>
        </v-tabs>

        <div class="d-flex align-center ga-2">
          <!-- Dark / Light Theme Toggle Button -->
          <v-btn
            :icon="isDark ? 'mdi-weather-sunny' : 'mdi-weather-night'"
            variant="text"
            size="small"
            :title="isDark ? 'Beralih ke Light Mode' : 'Beralih ke Dark Mode'"
            @click="toggleTheme"
          />

          <!-- Button Menu Telegram Quick Actions -->
          <v-btn
            variant="tonal"
            color="primary"
            size="small"
            prepend-icon="mdi-robot-outline"
            @click="commandMenuDialog = true"
          >
            Menu Telegram
          </v-btn>

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
                        Tokens Dihemat (Cache)
                      </div>
                      <div class="text-h4 font-weight-bold mt-1 text-info">
                        {{ statsTokensSaved.toLocaleString() }}
                      </div>
                    </div>
                    <v-avatar color="info" variant="tonal" size="48" rounded="lg">
                      <v-icon size="28">mdi-piggy-bank-outline</v-icon>
                    </v-avatar>
                  </div>
                </v-card>
              </v-col>
            </v-row>

            <!-- Activities Table Card -->
            <v-card color="surface" class="overflow-hidden">
              <v-card-title class="d-flex flex-wrap align-center justify-space-between ga-3 pa-4 pa-sm-6 border-b">
                <div class="d-flex align-center">
                  <v-icon class="me-2 text-primary">mdi-format-list-bulleted</v-icon>
                  <span class="font-weight-bold text-h6">Riwayat Audit & Aktivitas</span>
                </div>

                <div class="d-flex flex-wrap align-center ga-3">
                  <v-text-field
                    v-model="searchQuery"
                    placeholder="Cari prompt, user, model..."
                    prepend-inner-icon="mdi-magnify"
                    density="compact"
                    variant="outlined"
                    hide-details
                    style="min-width: 220px;"
                    @keyup.enter="fetchActivities(1)"
                  />

                  <v-select
                    v-model="filterChannel"
                    :items="channelOptions"
                    density="compact"
                    variant="outlined"
                    hide-details
                    style="min-width: 150px;"
                    @update:model-value="fetchActivities(1)"
                  />

                  <v-select
                    v-model="filterStatus"
                    :items="statusOptions"
                    density="compact"
                    variant="outlined"
                    hide-details
                    style="min-width: 140px;"
                    @update:model-value="fetchActivities(1)"
                  />

                  <v-btn
                    color="primary"
                    variant="tonal"
                    icon="mdi-refresh"
                    density="comfortable"
                    :loading="loadingActivities"
                    @click="fetchActivities(currentPage)"
                  />
                </div>
              </v-card-title>

              <!-- Table -->
              <v-table density="comfortable" hover>
                <thead>
                  <tr>
                    <th class="text-left">Waktu</th>
                    <th class="text-left">Channel</th>
                    <th class="text-left">User</th>
                    <th class="text-left">Model / Provider</th>
                    <th class="text-left">Prompt Singkat</th>
                    <th class="text-center">Tokens</th>
                    <th class="text-center">Status</th>
                    <th class="text-center">Aksi</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-if="loadingActivities">
                    <td colspan="8" class="text-center py-6 text-medium-emphasis">
                      <v-progress-circular indeterminate color="primary" size="24" class="me-2" />
                      Memuat data aktivitas...
                    </td>
                  </tr>
                  <tr v-else-if="activities.length === 0">
                    <td colspan="8" class="text-center py-6 text-medium-emphasis">
                      Belum ada catatan aktivitas yang cocok dengan filter.
                    </td>
                  </tr>
                  <tr v-for="item in activities" :key="item.id">
                    <td class="text-caption font-monospace text-medium-emphasis">
                      {{ formatTime(item.timestamp) }}
                    </td>
                    <td>
                      <v-chip size="x-small" color="primary" variant="flat" class="font-weight-medium">
                        {{ item.channel_type }}
                      </v-chip>
                    </td>
                    <td>
                      <div class="text-caption font-weight-bold">{{ item.user_name || 'Anonim' }}</div>
                      <div class="text-caption text-medium-emphasis font-monospace">{{ item.user_id }}</div>
                    </td>
                    <td>
                      <div class="text-caption font-weight-medium">{{ item.model }}</div>
                      <div class="text-caption text-medium-emphasis">{{ item.provider }}</div>
                    </td>
                    <td style="max-width: 300px;">
                      <div class="text-caption text-truncate">{{ item.client_request }}</div>
                    </td>
                    <td class="text-center text-caption font-monospace">
                      {{ item.total_tokens || 0 }}
                      <span v-if="item.tokens_saved > 0" class="text-info text-caption">
                        (+{{ item.tokens_saved }})
                      </span>
                    </td>
                    <td class="text-center">
                      <v-chip
                        size="x-small"
                        :color="item.status === 'success' ? 'success' : 'error'"
                        variant="tonal"
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

          <!-- TAB 2: AI Chat Assistant with Fixed Scroll Area, Hierarchical Provider => Model, & Set Thinking -->
          <v-window-item value="chat">
            <v-row
              no-gutters
              class="rounded-xl overflow-hidden border"
              style="height: calc(100vh - 120px); min-height: 500px;"
            >
              <!-- LEFT: Topics Sidebar -->
              <v-col
                v-if="showDesktopTopicList || showMobileTopicList"
                cols="12"
                md="4"
                lg="3"
                class="bg-surface border-e d-flex flex-column"
                style="height: 100%; min-height: 0; max-height: 100%;"
                :class="{ 'd-none d-md-flex': !showMobileTopicList && showDesktopTopicList, 'd-flex': showMobileTopicList }"
              >
                <!-- Topic Header -->
                <div class="pa-3 border-b d-flex justify-space-between align-center flex-shrink-0">
                  <div class="d-flex align-center">
                    <v-icon color="primary" class="me-2">mdi-forum</v-icon>
                    <span class="font-weight-bold text-subtitle-2">Topik & Sesi Channel</span>
                  </div>
                  <div class="d-flex align-center ga-1">
                    <v-btn
                      color="primary"
                      size="small"
                      variant="tonal"
                      prepend-icon="mdi-plus"
                      @click="newTopicDialog = true"
                    >
                      Topik Baru
                    </v-btn>
                    <v-btn
                      icon="mdi-close"
                      size="x-small"
                      variant="text"
                      class="d-md-none"
                      @click="showMobileTopicList = false"
                    />
                  </div>
                </div>

                <!-- Channel Filter Selector -->
                <div class="pa-3 border-b bg-surface-variant flex-shrink-0">
                  <div class="text-caption text-medium-emphasis mb-1 font-weight-bold">Filter Channel:</div>
                  <v-select
                    v-model="selectedTopicChannel"
                    :items="topicChannelOptions"
                    density="compact"
                    variant="outlined"
                    hide-details
                    @update:model-value="fetchTopics"
                  />
                </div>

                <!-- Topic List Scroll Area -->
                <div class="flex-grow-1 overflow-y-auto pa-2" style="min-height: 0;">
                  <div v-if="loadingTopics" class="text-center py-6">
                    <v-progress-circular indeterminate color="primary" size="24" />
                    <div class="text-caption text-medium-emphasis mt-2">Memuat topik percakapan...</div>
                  </div>
                  <div v-else-if="topicList.length === 0" class="text-center py-6 text-medium-emphasis text-caption">
                    Belum ada sesi/topik di channel ini.
                  </div>
                  <v-list v-else density="compact" nav class="pa-0">
                    <v-list-item
                      v-for="t in topicList"
                      :key="t.id"
                      :active="activeTopicId === t.id"
                      rounded="lg"
                      class="mb-1"
                      color="primary"
                      @click="selectTopic(t)"
                    >
                      <template #prepend>
                        <v-avatar size="28" :color="activeTopicId === t.id ? 'primary' : 'surface-variant'" class="me-2">
                          <v-icon size="16">
                            {{ t.channel_id === 'admin' ? 'mdi-shield-account' : t.channel_id === 'telegram' ? 'mdi-telegram' : t.channel_id === 'whatsapp' ? 'mdi-whatsapp' : 'mdi-message-text' }}
                          </v-icon>
                        </v-avatar>
                      </template>
                      <v-list-item-title class="font-weight-medium text-body-2">
                        {{ t.title || 'Topik Tanpa Judul' }}
                      </v-list-item-title>
                      <v-list-item-subtitle class="text-caption d-flex align-center ga-1 mt-1">
                        <v-chip size="x-small" density="compact" variant="flat" color="surface-variant">
                          {{ t.channel_id }}
                        </v-chip>
                        <span class="text-truncate">{{ formatTime(t.updated_at) }}</span>
                      </v-list-item-subtitle>
                    </v-list-item>
                  </v-list>
                </div>
              </v-col>

              <!-- RIGHT: Chat Window -->
              <v-col
                cols="12"
                :md="showDesktopTopicList ? 8 : 12"
                :lg="showDesktopTopicList ? 9 : 12"
                class="d-flex flex-column bg-surface"
                style="height: 100%; min-height: 0; max-height: 100%; overflow: hidden;"
              >
                <!-- Chat Header Bar: Provider, Model & Set Thinking -->
                <div class="pa-3 border-b d-flex flex-wrap justify-space-between align-center ga-2 bg-surface flex-shrink-0">
                  <div class="d-flex align-center ga-2">
                    <v-btn
                      icon="mdi-menu"
                      variant="text"
                      size="small"
                      class="d-md-none"
                      title="Buka Daftar Topik"
                      @click="showMobileTopicList = !showMobileTopicList"
                    />
                    <v-btn
                      :icon="showDesktopTopicList ? 'mdi-dock-left' : 'mdi-dock-window'"
                      variant="text"
                      size="small"
                      class="d-none d-md-flex"
                      :title="showDesktopTopicList ? 'Sembunyikan Panel Topik' : 'Tampilkan Panel Topik'"
                      @click="showDesktopTopicList = !showDesktopTopicList"
                    />

                    <div>
                      <div class="d-flex align-center ga-2">
                        <span class="font-weight-bold text-subtitle-1">{{ currentTopicTitle }}</span>
                        <v-chip size="x-small" color="primary" variant="tonal">
                          {{ currentTopicChannel }}
                        </v-chip>
                      </div>
                      <div class="text-caption text-medium-emphasis">
                        {{ chatSubtitle }}
                      </div>
                    </div>
                  </div>

                  <!-- Hierarchical Provider => Model Selection & Thinking Control -->
                  <div class="d-flex align-center flex-wrap ga-2">
                    <!-- 1. Provider Selector -->
                    <div style="min-width: 160px;">
                      <v-select
                        v-model="selectedProviderId"
                        :items="providerOptions"
                        item-title="title"
                        item-value="value"
                        label="Penyedia AI"
                        density="compact"
                        variant="outlined"
                        hide-details
                        prepend-inner-icon="mdi-server"
                        @update:model-value="onProviderChange"
                      />
                    </div>

                    <!-- 2. Model Selector (Under Selected Provider) -->
                    <div style="min-width: 220px;">
                      <v-select
                        v-model="selectedModel"
                        :items="availableModelsForSelectedProvider"
                        item-title="name"
                        item-value="id"
                        label="Model AI"
                        density="compact"
                        variant="outlined"
                        hide-details
                        prepend-inner-icon="mdi-brain"
                        @update:model-value="onModelChange"
                      />
                    </div>

                    <!-- 3. Thinking Button (Visible when model supports thinking) -->
                    <v-btn
                      v-if="currentModelThinkingConfig && currentModelThinkingConfig.supported"
                      variant="tonal"
                      color="amber"
                      size="small"
                      prepend-icon="mdi-brain"
                      class="font-weight-medium"
                      title="Atur parameter proses berpikir model ini"
                      @click="thinkingDialog = true"
                    >
                      {{ currentThinkingValueLabel }}
                    </v-btn>

                    <!-- Stop AI Button (Active during processing) -->
                    <v-btn
                      v-if="chatStreaming"
                      color="error"
                      variant="flat"
                      size="small"
                      prepend-icon="mdi-stop-circle"
                      class="font-weight-bold animate-pulse"
                      @click="handleStopChat"
                    >
                      Stop AI (/stop)
                    </v-btn>

                    <!-- Reset Chat -->
                    <v-btn
                      variant="tonal"
                      color="warning"
                      size="small"
                      prepend-icon="mdi-delete-sweep-outline"
                      @click="handleResetChat"
                    >
                      Reset
                    </v-btn>
                  </div>
                </div>

                <!-- Chat Message Scroll Area (Explicit min-height: 0 and flex: 1 1 0 enables proper scrolling) -->
                <div
                  ref="chatScrollRef"
                  class="chat-scroll-area pa-4 pa-sm-6 d-flex flex-column ga-4"
                  style="min-height: 0; flex: 1 1 0; overflow-y: auto; overflow-x: hidden;"
                >
                  <div
                    v-for="(msg, idx) in chatMessages"
                    :key="idx"
                    class="d-flex ga-3"
                    :class="msg.role === 'user' ? 'justify-end' : 'justify-start'"
                  >
                    <!-- Assistant Avatar -->
                    <v-avatar
                      v-if="msg.role === 'assistant'"
                      color="success"
                      size="36"
                      class="flex-shrink-0 mt-1"
                    >
                      <v-icon size="20" color="white">mdi-robot</v-icon>
                    </v-avatar>

                    <!-- Bubble Card -->
                    <div style="max-width: 85%;">
                      <!-- Thinking Details if present -->
                      <v-expansion-panels v-if="msg.thinking" v-model="msg.thinkingOpen" class="mb-2" variant="inset">
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
                        :color="msg.role === 'user' ? 'primary' : undefined"
                        :class="msg.role === 'user' ? 'bubble-user' : 'bubble-assistant'"
                        class="pa-3 pa-sm-4 bubble-card"
                        elevation="1"
                      >
                        <!-- Live SSE Status Indicator inside Chat Bubble -->
                        <div
                          v-if="msg.status && (chatStreaming || !msg.content)"
                          class="d-flex align-center ga-2 text-caption mb-2 text-warning font-weight-medium bg-black-opacity pa-2 rounded"
                        >
                          <v-progress-circular indeterminate size="14" width="2" color="warning" />
                          <span>{{ msg.status }}</span>
                        </div>

                        <!-- Markdown Content -->
                        <div v-if="msg.content" class="markdown-body" v-html="renderMarkdown(msg.content)" />
                        <div v-else-if="!msg.status" class="text-caption text-medium-emphasis">
                          Menunggu respon AI...
                        </div>
                      </v-card>
                    </div>

                    <!-- User Avatar -->
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
                <div class="pa-4 border-t bg-surface flex-shrink-0">
                  <div class="d-flex align-end ga-2">
                    <v-textarea
                      v-model="chatInput"
                      placeholder="Ketik pesan atau /stop untuk membatalkan (Enter kirim, Shift+Enter baris baru)..."
                      rows="1"
                      auto-grow
                      max-rows="5"
                      hide-details
                      variant="outlined"
                      density="comfortable"
                      class="flex-grow-1"
                      @keydown.enter.exact.prevent="sendChatMessage"
                    />

                    <!-- Send Button or Stop Button -->
                    <v-btn
                      v-if="!chatStreaming"
                      color="primary"
                      icon="mdi-send"
                      size="large"
                      :disabled="!chatInput.trim()"
                      @click="sendChatMessage"
                    />
                    <v-btn
                      v-else
                      color="error"
                      variant="flat"
                      icon="mdi-stop-circle"
                      size="large"
                      title="Batalkan proses AI (/stop)"
                      @click="handleStopChat"
                    />
                  </div>
                </div>
              </v-col>
            </v-row>
          </v-window-item>

          <!-- TAB 3: Status & Konfigurasi Jaringan Server -->
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

              <!-- Web Admin Network Binding & Port Configuration Card -->
              <v-col cols="12" md="6">
                <v-card color="surface" class="pa-6">
                  <div class="d-flex align-center mb-4">
                    <v-avatar color="secondary" variant="tonal" size="44" class="me-3">
                      <v-icon size="26">mdi-cog-sync-outline</v-icon>
                    </v-avatar>
                    <div>
                      <h3 class="text-h6 font-weight-bold">Konfigurasi Jaringan & Port Web Admin</h3>
                      <div class="text-caption text-medium-emphasis">Pengaturan binding address dan port dinamis</div>
                    </div>
                  </div>

                  <v-alert type="info" variant="tonal" class="mb-4" density="comfortable">
                    Pengaturan ini dapat diatur di sini atau sewaktu-waktu via Telegram bot:
                    <div class="mt-1">
                      <code class="text-white font-weight-bold me-2">/setwebport &lt;port&gt;</code>
                      <code class="text-white font-weight-bold">/setwebbind &lt;address&gt;</code>
                    </div>
                  </v-alert>

                  <v-card color="surface-variant" class="pa-4 mb-4" rounded="lg">
                    <div class="d-flex justify-space-between align-center">
                      <div>
                        <div class="text-caption text-medium-emphasis text-uppercase">Binding Address:</div>
                        <div class="text-h5 font-weight-bold font-monospace text-primary">
                          {{ currentBindAddress }}
                        </div>
                      </div>
                      <div>
                        <div class="text-caption text-medium-emphasis text-uppercase text-end">Port Aktif:</div>
                        <div class="text-h5 font-weight-bold font-monospace text-primary text-end">
                          {{ currentWebPort }}
                        </div>
                      </div>
                    </div>
                    <v-divider class="my-2" />
                    <div class="text-caption text-medium-emphasis font-monospace text-truncate">
                      URL Akses: <a :href="currentHostUrl" target="_blank" class="text-info">{{ currentHostUrl }}</a>
                    </div>
                  </v-card>

                  <!-- Binding Address Input -->
                  <div class="mb-3">
                    <v-text-field
                      v-model="bindAddressInput"
                      label="Binding Address (Host/IP)"
                      prepend-inner-icon="mdi-ip-network-outline"
                      placeholder="Contoh: 0.0.0.0 atau 127.0.0.1"
                      hide-details
                      class="mb-2"
                    />
                    <div class="d-flex ga-2">
                      <v-chip size="small" variant="tonal" @click="bindAddressInput = '0.0.0.0'">
                        0.0.0.0 (Semua Jaringan)
                      </v-chip>
                      <v-chip size="small" variant="tonal" @click="bindAddressInput = '127.0.0.1'">
                        127.0.0.1 (Localhost Saja)
                      </v-chip>
                    </div>
                  </div>

                  <!-- Port Input -->
                  <v-text-field
                    v-model="newPortInput"
                    label="Nomor Port Baru (1024 - 65535)"
                    type="number"
                    min="1024"
                    max="65535"
                    prepend-inner-icon="mdi-numeric"
                    placeholder="Contoh: 8088 atau 12111"
                    class="mb-4"
                  />

                  <v-btn
                    color="primary"
                    block
                    size="large"
                    prepend-icon="mdi-content-save-cog"
                    :loading="updatingAddress"
                    @click="handleSaveAddress"
                  >
                    Simpan & Terapkan Perubahan Jaringan
                  </v-btn>
                </v-card>
              </v-col>
            </v-row>
          </v-window-item>
        </v-window>
      </v-container>
    </v-main>

    <!-- Dialog: Pengaturan Berpikir Model AI (Set Thinking) -->
    <v-dialog v-model="thinkingDialog" max-width="560">
      <v-card color="surface" rounded="xl" class="pa-4 pa-sm-6" v-if="currentModelThinkingConfig">
        <div class="d-flex justify-space-between align-center border-b pb-3 mb-4">
          <div class="d-flex align-center">
            <v-avatar color="amber" variant="tonal" size="40" class="me-3">
              <v-icon size="24">mdi-brain</v-icon>
            </v-avatar>
            <div>
              <div class="font-weight-bold text-h6">Pengaturan Berpikir AI</div>
              <div class="text-caption text-medium-emphasis">
                {{ currentModelDetail ? currentModelDetail.name : selectedModel }}
              </div>
            </div>
          </div>
          <v-btn icon="mdi-close" variant="text" size="small" @click="thinkingDialog = false" />
        </div>

        <!-- Parameter Info -->
        <div class="mb-4">
          <div class="text-caption text-medium-emphasis font-weight-bold text-uppercase mb-1">
            Parameter API: <span class="font-monospace text-primary">{{ currentModelThinkingConfig.parameter_name }}</span>
          </div>
          <div v-if="currentModelThinkingConfig.note" class="text-caption text-medium-emphasis mb-3 bg-surface-variant pa-2 rounded">
            💡 {{ currentModelThinkingConfig.note }}
          </div>
        </div>

        <!-- Options Selection: String Options -->
        <div v-if="currentModelThinkingConfig.parameter_type === 'string'" class="d-flex flex-column ga-2 mb-4">
          <v-card
            v-for="opt in currentModelThinkingConfig.options"
            :key="opt.value"
            :color="thinkingLevel === opt.value ? 'primary' : 'surface-variant'"
            :variant="thinkingLevel === opt.value ? 'tonal' : 'flat'"
            class="pa-3 cursor-pointer border"
            rounded="lg"
            @click="thinkingLevel = opt.value"
          >
            <div class="d-flex justify-space-between align-center mb-1">
              <span class="font-weight-bold text-subtitle-2">{{ opt.label }} (<code>{{ opt.value }}</code>)</span>
              <v-icon v-if="thinkingLevel === opt.value" color="primary">mdi-check-circle</v-icon>
            </div>
            <div class="text-caption" :class="thinkingLevel === opt.value ? 'text-white' : 'text-medium-emphasis'">
              {{ opt.description }}
            </div>
          </v-card>
        </div>

        <!-- Options Selection: Object (Anthropic budget_tokens) -->
        <div v-else-if="currentModelThinkingConfig.parameter_type === 'object'" class="mb-4">
          <div class="text-caption text-medium-emphasis mb-2 font-weight-bold">
            Pilih Kuota Token Berpikir (Budget Tokens):
          </div>
          <v-row dense class="mb-3">
            <v-col cols="6" sm="3" v-for="opt in currentModelThinkingConfig.options" :key="opt.value">
              <v-btn
                block
                :color="thinkingBudget === parseInt(opt.value, 10) ? 'primary' : 'surface-variant'"
                :variant="thinkingBudget === parseInt(opt.value, 10) ? 'flat' : 'tonal'"
                size="small"
                @click="thinkingBudget = parseInt(opt.value, 10)"
              >
                {{ opt.label }}
              </v-btn>
            </v-col>
          </v-row>
          <v-text-field
            v-model.number="thinkingBudget"
            label="Custom Token Budget (Integer)"
            type="number"
            min="1024"
            max="65536"
            step="1024"
            density="compact"
            variant="outlined"
            hide-details
          />
        </div>

        <div class="d-flex justify-end ga-2 pt-3 border-t">
          <v-btn variant="text" @click="thinkingDialog = false">Tutup</v-btn>
          <v-btn color="primary" prepend-icon="mdi-check" @click="thinkingDialog = false">
            Terapkan Pengaturan
          </v-btn>
        </div>
      </v-card>
    </v-dialog>

    <!-- Dialog: Menu Command Telegram Quick Actions & Reference -->
    <v-dialog v-model="commandMenuDialog" max-width="850">
      <v-card color="surface" rounded="xl">
        <v-card-title class="d-flex justify-space-between align-center pa-4 pa-sm-6 border-b">
          <div class="d-flex align-center">
            <v-avatar color="primary" variant="tonal" size="40" class="me-3">
              <v-icon size="24">mdi-robot</v-icon>
            </v-avatar>
            <div>
              <span class="font-weight-bold text-h6">Menu Perintah Bot Telegram</span>
              <div class="text-caption text-medium-emphasis">Aksi cepat dan referensi perintah lengkap administrator</div>
            </div>
          </div>
          <v-btn icon="mdi-close" variant="text" size="small" @click="commandMenuDialog = false" />
        </v-card-title>

        <v-card-text class="pa-4 pa-sm-6 overflow-y-auto" style="max-height: 70vh;">
          <!-- Quick Action Buttons -->
          <div class="text-subtitle-2 font-weight-bold mb-3 d-flex align-center">
            <v-icon color="warning" class="me-2">mdi-lightning-bolt</v-icon>
            Aksi Cepat (Quick Actions)
          </div>
          <v-row class="mb-4" dense>
            <v-col cols="6" sm="4">
              <v-btn color="error" variant="tonal" block prepend-icon="mdi-stop-circle" @click="handleQuickStop">
                /stop AI
              </v-btn>
            </v-col>
            <v-col cols="6" sm="4">
              <v-btn color="primary" variant="tonal" block prepend-icon="mdi-plus" @click="commandMenuDialog = false; newTopicDialog = true">
                /new Topik
              </v-btn>
            </v-col>
            <v-col cols="6" sm="4">
              <v-btn color="info" variant="tonal" block prepend-icon="mdi-server" @click="commandMenuDialog = false; activeTab = 'system'">
                /status Server
              </v-btn>
            </v-col>
            <v-col cols="6" sm="4">
              <v-btn color="warning" variant="tonal" block prepend-icon="mdi-delete-sweep" @click="handleResetChat">
                /clear Chat
              </v-btn>
            </v-col>
            <v-col cols="6" sm="4">
              <v-btn color="secondary" variant="tonal" block prepend-icon="mdi-cog" @click="commandMenuDialog = false; activeTab = 'system'">
                /setwebport
              </v-btn>
            </v-col>
            <v-col cols="6" sm="4">
              <v-btn color="success" variant="tonal" block prepend-icon="mdi-ip" @click="commandMenuDialog = false; activeTab = 'system'">
                /setwebbind
              </v-btn>
            </v-col>
          </v-row>

          <v-divider class="mb-4" />

          <!-- Full Telegram Commands Table -->
          <div class="text-subtitle-2 font-weight-bold mb-3 d-flex align-center">
            <v-icon color="primary" class="me-2">mdi-clipboard-text-outline</v-icon>
            Daftar Perintah Telegram GoAssistant
          </div>
          <v-table density="compact" class="border rounded-lg">
            <thead>
              <tr>
                <th class="text-left">Perintah</th>
                <th class="text-left">Kategori</th>
                <th class="text-left">Fungsi / Kegunaan</th>
                <th class="text-center">Salin</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="cmd in telegramCommands" :key="cmd.command">
                <td class="font-monospace text-primary font-weight-bold">{{ cmd.command }}</td>
                <td><v-chip size="x-small" variant="tonal">{{ cmd.category }}</v-chip></td>
                <td class="text-caption">{{ cmd.description }}</td>
                <td class="text-center">
                  <v-btn
                    icon="mdi-content-copy"
                    size="x-small"
                    variant="text"
                    title="Salin Perintah"
                    @click="copyToClipboard(cmd.command)"
                  />
                </td>
              </tr>
            </tbody>
          </v-table>
        </v-card-text>
      </v-card>
    </v-dialog>

    <!-- Dialog: Buat Topik Baru -->
    <v-dialog v-model="newTopicDialog" max-width="460">
      <v-card color="surface" rounded="xl" class="pa-4 pa-sm-6">
        <v-card-title class="font-weight-bold text-h6 px-0 pb-2">Buat Topik / Sesi Baru</v-card-title>
        <p class="text-caption text-medium-emphasis mb-4">
          Buat sesi percakapan mandiri yang terpisah agar riwayat konteks tetap rapi dan terisolasi.
        </p>

        <v-text-field
          v-model="newTopicTitle"
          label="Judul Topik"
          placeholder="Contoh: Diskusi Deployment atau Riset Model"
          variant="outlined"
          autofocus
          class="mb-3"
          @keyup.enter="handleCreateTopic"
        />

        <v-select
          v-model="newTopicChannel"
          label="Target Channel"
          :items="channelCreateOptions"
          variant="outlined"
          class="mb-4"
        />

        <div class="d-flex justify-end ga-2">
          <v-btn variant="text" @click="newTopicDialog = false">Batal</v-btn>
          <v-btn color="primary" :loading="creatingTopic" @click="handleCreateTopic">
            Buat Topik Sekarang
          </v-btn>
        </div>
      </v-card>
    </v-dialog>

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
import { useTheme } from 'vuetify'
import MarkdownIt from 'markdown-it'

const theme = useTheme()
const isDark = computed(() => theme.global.current.value.dark)

function toggleTheme() {
  const nextTheme = isDark.value ? 'light' : 'dark'
  theme.global.name.value = nextTheme
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem('goassistant_theme', nextTheme)
  }
}

const md = new MarkdownIt({
  html: false,
  linkify: true,
  breaks: true,
})

// Authentication State
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

// Activity Logs State
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

// Topic & Multi-Channel State
const topicList = ref([])
const loadingTopics = ref(false)
const selectedTopicChannel = ref('all')
const activeTopicId = ref('')
const currentTopicTitle = ref('Topik Utama (Admin Telegram)')
const currentTopicChannel = ref('admin')
const showDesktopTopicList = ref(true)
const showMobileTopicList = ref(false)
const newTopicDialog = ref(false)
const newTopicTitle = ref('')
const newTopicChannel = ref('admin')
const creatingTopic = ref(false)

const topicChannelOptions = ref([
  { title: 'Semua Channel', value: 'all' },
  { title: 'Admin Telegram (admin)', value: 'admin' },
  { title: 'Web Admin (webadmin)', value: 'webadmin' },
  { title: 'Telegram Channel', value: 'telegram' },
  { title: 'WhatsApp Channel', value: 'whatsapp' }
])

const channelCreateOptions = [
  { title: 'Admin Telegram (admin)', value: 'admin' },
  { title: 'Web Admin (webadmin)', value: 'webadmin' }
]

// Hierarchical Provider => Model State
const rawProvidersData = ref([])
const selectedProviderId = ref('')
const selectedModel = ref('')

// Thinking Configuration State
const thinkingDialog = ref(false)
const thinkingLevel = ref('medium')
const thinkingBudget = ref(4096)

const providerOptions = computed(() => {
  if (!rawProvidersData.value || rawProvidersData.value.length === 0) {
    return []
  }
  return rawProvidersData.value.map(p => ({
    title: p.name,
    value: p.id
  }))
})

const availableModelsForSelectedProvider = computed(() => {
  if (!rawProvidersData.value || rawProvidersData.value.length === 0) {
    return [{ id: selectedModel.value, name: selectedModel.value }]
  }
  const currentProv = rawProvidersData.value.find(p => p.id === selectedProviderId.value)
  if (!currentProv || !currentProv.models) return []
  return currentProv.models
})

const currentModelDetail = computed(() => {
  if (!availableModelsForSelectedProvider.value) return null
  return availableModelsForSelectedProvider.value.find(m => m.id === selectedModel.value) || null
})

const currentModelThinkingConfig = computed(() => {
  if (currentModelDetail.value && currentModelDetail.value.thinking_config) {
    return currentModelDetail.value.thinking_config
  }
  return null
})

const currentThinkingValueLabel = computed(() => {
  if (!currentModelThinkingConfig.value) return ''
  if (currentModelThinkingConfig.value.parameter_type === 'object') {
    return `Thinking: ${thinkingBudget.value} Tokens`
  }
  return `Thinking: ${thinkingLevel.value.toUpperCase()}`
})

function onProviderChange(provId) {
  if (!provId) return
  selectedProviderId.value = provId
  const currentProv = rawProvidersData.value.find(p => p.id === provId)
  if (currentProv && currentProv.models && currentProv.models.length > 0) {
    selectedModel.value = currentProv.models[0].id
    onModelChange(selectedModel.value)
  }
}

function onModelChange(modelId) {
  if (!modelId) return
  selectedModel.value = modelId
  const modelDetail = availableModelsForSelectedProvider.value.find(m => m.id === modelId)
  if (modelDetail && modelDetail.thinking_config) {
    const cfg = modelDetail.thinking_config
    if (cfg.default_value) {
      if (cfg.parameter_type === 'object') {
        thinkingBudget.value = parseInt(cfg.default_value, 10) || 4096
      } else {
        thinkingLevel.value = cfg.default_value
      }
    }
  }
}

// Chat Messages State
const chatMessages = ref([
  {
    role: 'assistant',
    content: 'Halo Administrator! Saya adalah asisten AI GoAssistant. Ada yang bisa saya bantu terkait monitoring, analisis log, atau konfigurasi sistem?',
    status: '',
    thinking: ''
  }
])
const chatInput = ref('')
const chatStreaming = ref(false)
const chatSubtitle = ref('Terkoneksi langsung ke AI Orchestrator')
const chatScrollRef = ref(null)
let activeAbortController = null

// System, Binding & Port State
const sysStats = ref({})
const currentWebPort = ref(12111)
const currentBindAddress = ref('0.0.0.0')
const currentHostUrl = ref('')
const bindAddressInput = ref('0.0.0.0')
const newPortInput = ref('')
const updatingAddress = ref(false)

// Telegram Command Dialog State
const commandMenuDialog = ref(false)
const telegramCommands = [
  { command: '/start', category: 'Navigasi', description: 'Buka menu utama dashboard administrator di bot Telegram' },
  { command: '/status', category: 'Sistem', description: 'Cek kesehatan server, uptime, memory, dan model AI aktif' },
  { command: '/models', category: 'AI Model', description: 'Pilih dan ganti model AI default atau combo model fallback' },
  { command: '/providers', category: 'Penyedia', description: 'Kelola API key, status provider AI (Gemini, OpenAI, Anthropic, dll)' },
  { command: '/topic', category: 'Topik', description: 'Kelola topik obrolan multi-thread percakapan' },
  { command: '/newtopic', category: 'Topik', description: 'Buat sesi/topik percakapan baru di Telegram' },
  { command: '/stop', category: 'Kontrol AI', description: 'Hentikan seketika proses AI atau tool yang sedang berjalan' },
  { command: '/limits', category: 'Keamanan', description: 'Atur batas token, turn history, dan rate limit' },
  { command: '/proxies', category: 'Jaringan', description: 'Kelola pool proxy, sinkronisasi Webshare, uji latency' },
  { command: '/setwebport', category: 'Web Admin', description: 'Ganti port listener Web Admin secara dinamis' },
  { command: '/setwebbind', category: 'Web Admin', description: 'Ganti binding IP address Web Admin (0.0.0.0 / 127.0.0.1)' },
  { command: '/backup', category: 'Database', description: 'Cadangkan database SQLite GoAssistant' },
  { command: '/update', category: 'Sistem', description: 'Periksa pembaruan versi rilis GoAssistant' }
]

// Snackbar State
const snackbar = ref({
  show: false,
  text: '',
  color: 'info'
})

function showToast(text, color = 'info') {
  snackbar.value = { show: true, text, color }
}

function copyToClipboard(text) {
  navigator.clipboard.writeText(text).then(() => {
    showToast(`Perintah ${text} berhasil disalin!`, 'success')
  })
}

// Computed Statistics
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
      initDashboard()
    } else {
      loginRequired.value = true
    }
  } catch (e) {
    loginRequired.value = true
  }
}

function initDashboard() {
  fetchActivities(1)
  fetchSystemStats()
  fetchTopics()
  fetchModels()
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
      initDashboard()
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

// Topic & Session Handlers
async function fetchTopics() {
  loadingTopics.value = true
  try {
    const chParam = selectedTopicChannel.value === 'all' ? '' : selectedTopicChannel.value
    const res = await fetch('/api/topics?channel_id=' + encodeURIComponent(chParam))
    if (!res.ok) return
    const data = await res.json()

    // Update channel options if server returned channels
    if (data.channels && data.channels.length > 0) {
      const opts = [{ title: 'Semua Channel', value: 'all' }]
      for (const ch of data.channels) {
        opts.push({ title: `${ch} Channel`, value: ch })
      }
      topicChannelOptions.value = opts
    }

    if (selectedTopicChannel.value === 'all') {
      topicList.value = data.all_topics || []
    } else if (selectedTopicChannel.value === 'admin') {
      topicList.value = data.admin_topics || []
    } else if (selectedTopicChannel.value === 'webadmin') {
      topicList.value = data.web_topics || []
    } else {
      topicList.value = data.all_topics || []
    }

    // Set first topic active if none selected
    if (!activeTopicId.value && topicList.value.length > 0) {
      const active = topicList.value.find(t => t.is_active) || topicList.value[0]
      selectTopic(active)
    }
  } catch (e) {
  } finally {
    loadingTopics.value = false
  }
}

async function selectTopic(topic) {
  activeTopicId.value = topic.id
  currentTopicTitle.value = topic.title || 'Topik Sesi'
  currentTopicChannel.value = topic.channel_id
  showMobileTopicList.value = false

  try {
    const res = await fetch(`/api/topics/messages?session_id=${topic.id}`)
    if (res.ok) {
      const data = await res.json()
      if (data.messages && data.messages.length > 0) {
        chatMessages.value = data.messages.map(m => ({
          role: m.role,
          content: m.content,
          status: '',
          thinking: ''
        }))
      } else {
        chatMessages.value = [
          {
            role: 'assistant',
            content: `Sesi **${topic.title}** aktif. Silakan mulai percakapan!`,
            status: '',
            thinking: ''
          }
        ]
      }
      scrollChatBottom()
    }
  } catch (e) {}
}

async function handleCreateTopic() {
  const title = newTopicTitle.value.trim()
  if (!title) {
    showToast('Judul topik tidak boleh kosong', 'warning')
    return
  }

  creatingTopic.value = true
  try {
    const res = await fetch('/api/topics/new', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title,
        channel_id: newTopicChannel.value
      })
    })
    const data = await res.json()
    if (res.ok && data.session) {
      showToast(`Topik "${title}" berhasil dibuat!`, 'success')
      newTopicDialog.value = false
      newTopicTitle.value = ''
      await fetchTopics()
      selectTopic(data.session)
    } else {
      showToast(data.error || 'Gagal membuat topik', 'error')
    }
  } catch (e) {
    showToast('Error membuat topik: ' + e.message, 'error')
  } finally {
    creatingTopic.value = false
  }
}

// Models & Providers Handlers
async function fetchModels() {
  try {
    const res = await fetch('/api/models')
    if (res.ok) {
      const data = await res.json()
      if (data.providers && data.providers.length > 0) {
        rawProvidersData.value = data.providers

        // 1. Resolve Provider
        let targetProv = data.providers.find(p => p.id === selectedProviderId.value)
        if (!targetProv && data.active_provider) {
          targetProv = data.providers.find(p => p.id === data.active_provider)
        }
        if (!targetProv) {
          targetProv = data.providers[0]
        }
        selectedProviderId.value = targetProv.id

        // 2. Resolve Model
        let targetModel = targetProv.models && targetProv.models.find(m => m.id === selectedModel.value)
        if (!targetModel && data.active_model) {
          targetModel = targetProv.models && targetProv.models.find(m => m.id === data.active_model)
        }
        if (!targetModel && targetProv.models && targetProv.models.length > 0) {
          targetModel = targetProv.models[0]
        }
        if (targetModel) {
          selectedModel.value = targetModel.id
          onModelChange(selectedModel.value)
        }
      }
    }
  } catch (e) {
    console.error('Gagal mengambil daftar model/provider:', e)
  }
}

// Chat Handlers & Stop AI
async function sendChatMessage() {
  const text = chatInput.value.trim()
  if (!text || chatStreaming.value) return

  // Append user message
  chatMessages.value.push({ role: 'user', content: text, status: '', thinking: '' })
  chatInput.value = ''

  // Append empty assistant message with active status
  const assistantMsgIndex = chatMessages.value.length
  chatMessages.value.push({
    role: 'assistant',
    content: '',
    status: 'Memproses permintaan...',
    thinking: '',
    thinkingOpen: 0
  })

  chatStreaming.value = true
  chatSubtitle.value = 'Sedang memproses respon AI...'
  scrollChatBottom()

  activeAbortController = new AbortController()

  try {
    const response = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      signal: activeAbortController.signal,
      body: JSON.stringify({
        message: text,
        session_id: activeTopicId.value,
        channel_id: currentTopicChannel.value,
        model: selectedModel.value,
        provider: selectedProviderId.value,
        thinking_level: thinkingLevel.value,
        thinking_budget: thinkingBudget.value
      })
    })

    if (!response.ok) throw new Error('Server error: ' + response.statusText)

    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    let currentEvent = 'message'

    while (true) {
      const { value, done } = await reader.read()
      if (done) break

      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop()

      for (const line of lines) {
        const trimmed = line.trim()
        if (trimmed === '') {
          currentEvent = 'message'
          continue
        }

        if (line.startsWith('event:')) {
          currentEvent = line.slice(6).trim()
        } else if (line.startsWith('data:')) {
          const dataStr = line.slice(5).trim()
          if (!dataStr) continue

          try {
            const parsed = JSON.parse(dataStr)

            // SSE stream status & progress handling
            if ((currentEvent === 'start' || currentEvent === 'progress') && parsed.status) {
              chatMessages.value[assistantMsgIndex].status = parsed.status
              chatSubtitle.value = '⏳ ' + parsed.status
            } else if (currentEvent === 'thinking' && parsed.text) {
              chatMessages.value[assistantMsgIndex].thinking += parsed.text
              chatMessages.value[assistantMsgIndex].status = '💭 Model sedang berpikir...'
            } else if (currentEvent === 'chunk' && parsed.text) {
              chatMessages.value[assistantMsgIndex].status = '' // Clear status once content chunks stream
              chatMessages.value[assistantMsgIndex].content += parsed.text
              // Auto-collapse thinking panel when answer begins
              if (chatMessages.value[assistantMsgIndex].thinkingOpen === 0) {
                chatMessages.value[assistantMsgIndex].thinkingOpen = undefined
              }
            } else if (currentEvent === 'done') {
              chatMessages.value[assistantMsgIndex].status = ''
              if (!chatMessages.value[assistantMsgIndex].content && parsed.response) {
                chatMessages.value[assistantMsgIndex].content = parsed.response
              }
            } else if (currentEvent === 'error') {
              chatMessages.value[assistantMsgIndex].status = ''
              chatMessages.value[assistantMsgIndex].content += `\n\n> ⚠️ **Error:** ${parsed.error}`
            }
          } catch (e) {}
        }
      }
      scrollChatBottom()
    }
  } catch (err) {
    if (err.name === 'AbortError') {
      chatMessages.value[assistantMsgIndex].status = ''
      chatMessages.value[assistantMsgIndex].content += `\n\n🛑 *[Pemrosesan AI dihentikan oleh pengguna /stop]*`
    } else {
      chatMessages.value[assistantMsgIndex].status = ''
      chatMessages.value[assistantMsgIndex].content += `\n\n> ⚠️ **Koneksi terputus:** ${err.message}`
    }
  } finally {
    chatStreaming.value = false
    chatSubtitle.value = 'Terkoneksi langsung ke AI Orchestrator'
    activeAbortController = null
    scrollChatBottom()
  }
}

async function handleStopChat() {
  if (activeAbortController) {
    activeAbortController.abort()
  }
  try {
    await fetch('/api/chat/stop', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ session_id: activeTopicId.value })
    })
    showToast('Perintah /stop dikirimkan ke server', 'warning')
  } catch (e) {}
}

async function handleQuickStop() {
  await handleStopChat()
  commandMenuDialog.value = false
}

function scrollChatBottom() {
  nextTick(() => {
    if (chatScrollRef.value) {
      chatScrollRef.value.scrollTop = chatScrollRef.value.scrollHeight
    }
  })
}

async function handleResetChat() {
  if (!confirm('Bersihkan riwayat obrolan topik ini?')) return
  try {
    await fetch('/api/chat/clear', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ session_id: activeTopicId.value })
    })
    chatMessages.value = [
      {
        role: 'assistant',
        content: 'Riwayat obrolan berhasil dibersihkan. Silakan mulai pertanyaan baru!',
        status: '',
        thinking: ''
      }
    ]
    showToast('Riwayat chat berhasil direset', 'success')
  } catch (e) {
    showToast('Gagal mereset chat', 'error')
  }
}

// System, Binding & Port Handlers
async function fetchSystemStats() {
  try {
    const res = await fetch('/api/system/stats')
    if (!res.ok) return
    const data = await res.json()
    sysStats.value = data
    currentWebPort.value = data.current_web_port || 12111
    currentBindAddress.value = data.current_bind_address || '0.0.0.0'
    bindAddressInput.value = currentBindAddress.value
    newPortInput.value = currentWebPort.value.toString()
    const host = currentBindAddress.value === '0.0.0.0' ? window.location.hostname : currentBindAddress.value
    currentHostUrl.value = window.location.protocol + '//' + host + ':' + currentWebPort.value + '/admin'
  } catch (e) {}
}

async function handleSaveAddress() {
  const p = parseInt(newPortInput.value, 10)
  if (!p || p < 1024 || p > 65535) {
    alert('Nomor port harus berupa angka antara 1024 sampai 65535.')
    return
  }

  const bindAddr = bindAddressInput.value.trim() || '0.0.0.0'

  if (!confirm(`Konfirmasi: Pindahkan Web Admin ke ${bindAddr}:${p}? Halaman akan diarahkan ke alamat baru.`)) {
    return
  }

  updatingAddress.value = true
  try {
    const res = await fetch('/api/system/address', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        port: p,
        bind_address: bindAddr
      })
    })
    const data = await res.json()
    if (res.ok) {
      showToast(`Alamat berhasil diubah ke ${bindAddr}:${p}. Mengalihkan...`, 'success')
      const targetHost = bindAddr === '0.0.0.0' ? window.location.hostname : bindAddr
      setTimeout(() => {
        window.location.href = window.location.protocol + '//' + targetHost + ':' + p + '/admin'
      }, 1800)
    } else {
      alert('Gagal mengubah alamat: ' + (data.error || 'Unknown error'))
      updatingAddress.value = false
    }
  } catch (e) {
    showToast(`Server sedang berpindah ke ${bindAddr}:${p}...`, 'info')
    const targetHost = bindAddr === '0.0.0.0' ? window.location.hostname : bindAddr
    setTimeout(() => {
      window.location.href = window.location.protocol + '//' + targetHost + ':' + p + '/admin'
    }, 2000)
  }
}

onMounted(() => {
  if (typeof localStorage !== 'undefined') {
    const saved = localStorage.getItem('goassistant_theme')
    if (saved === 'light' || saved === 'dark') {
      theme.global.name.value = saved
    }
  }
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

.bg-black-opacity {
  background: rgba(0, 0, 0, 0.35);
}

.animate-pulse {
  animation: pulse 1.5s infinite;
}

@keyframes pulse {
  0% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.85; transform: scale(1.02); }
  100% { opacity: 1; transform: scale(1); }
}

/* Custom Visible Scrollbar for Chat */
.chat-scroll-area {
  min-height: 0 !important;
  flex: 1 1 0 !important;
  overflow-y: auto !important;
  scroll-behavior: smooth;
}

.chat-scroll-area::-webkit-scrollbar {
  width: 8px;
}

.chat-scroll-area::-webkit-scrollbar-track {
  background: rgba(255, 255, 255, 0.05);
  border-radius: 4px;
}

.chat-scroll-area::-webkit-scrollbar-thumb {
  background: rgba(255, 255, 255, 0.25);
  border-radius: 4px;
}

.chat-scroll-area::-webkit-scrollbar-thumb:hover {
  background: rgba(255, 255, 255, 0.45);
}

/* Assistant Bubble - Dark Theme */
.v-theme--dark .bubble-assistant {
  background-color: #1e293b !important;
  border: 1px solid rgba(255, 255, 255, 0.12) !important;
  color: #f8fafc !important;
}

/* Assistant Bubble - Light Theme */
.v-theme--light .bubble-assistant {
  background-color: #f8fafc !important;
  border: 1px solid #e2e8f0 !important;
  color: #0f172a !important;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.05) !important;
}

/* User Bubble */
.bubble-user {
  color: #ffffff !important;
}

/* Markdown typography inside chat bubble - High Contrast */
:deep(.markdown-body) {
  font-size: 0.925rem;
  line-height: 1.65;
  color: inherit !important;
}

:deep(.markdown-body p) {
  margin-bottom: 0.5rem;
  color: inherit !important;
}

:deep(.markdown-body p:last-child) {
  margin-bottom: 0;
}

:deep(.markdown-body h1),
:deep(.markdown-body h2),
:deep(.markdown-body h3),
:deep(.markdown-body h4) {
  margin-top: 0.75rem;
  margin-bottom: 0.5rem;
  font-weight: 700;
  color: inherit !important;
}

:deep(.markdown-body strong),
:deep(.markdown-body b) {
  font-weight: 700;
  color: inherit !important;
}

:deep(.markdown-body ul),
:deep(.markdown-body ol) {
  padding-left: 1.25rem;
  margin-bottom: 0.5rem;
  color: inherit !important;
}

:deep(.markdown-body li) {
  margin-bottom: 0.25rem;
  color: inherit !important;
}

/* Code styling inside Markdown */
.v-theme--dark :deep(.markdown-body code) {
  background: rgba(255, 255, 255, 0.12) !important;
  color: #38bdf8 !important;
  padding: 2px 6px;
  border-radius: 4px;
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.85em;
}

.v-theme--light :deep(.markdown-body code) {
  background: #e2e8f0 !important;
  color: #0369a1 !important;
  padding: 2px 6px;
  border-radius: 4px;
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.85em;
}

:deep(.markdown-body pre) {
  background: #0b1329 !important;
  color: #f8fafc !important;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 8px;
  padding: 10px 14px;
  margin: 8px 0;
  overflow-x: auto;
  font-family: 'JetBrains Mono', monospace;
  font-size: 0.825rem;
}

:deep(.markdown-body pre code) {
  background: transparent !important;
  color: inherit !important;
  padding: 0 !important;
}

/* Links inside Markdown */
.v-theme--dark :deep(.markdown-body a) {
  color: #818cf8 !important;
  text-decoration: underline;
}

.v-theme--light :deep(.markdown-body a) {
  color: #4f46e5 !important;
  text-decoration: underline;
}
</style>

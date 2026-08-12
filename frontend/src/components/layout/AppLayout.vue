<template>
  <slot v-if="!isRootLayout" />

  <div v-else class="app-layout min-h-screen bg-[#cdd8df] text-gray-800 dark:bg-[#061720] dark:text-gray-200 transition-colors duration-300">
    <DarkVideoBackground blurred />

    <!-- 科技感粒子动画背景 -->
    <canvas ref="appCanvasRef" class="pointer-events-none fixed inset-0 h-full w-full"></canvas>
    <div class="pointer-events-none fixed inset-0">
      <div class="absolute -top-40 right-0 h-[400px] w-[400px] rounded-full bg-primary-500/[0.04] blur-[100px] dark:bg-primary-500/[0.04]"></div>
      <div class="absolute bottom-0 left-0 h-[300px] w-[300px] rounded-full bg-cyan-500/[0.03] blur-[80px] dark:bg-cyan-500/[0.03]"></div>
    </div>


    <!-- Sidebar -->
    <AppSidebar />

    <!-- Main Content Area -->
    <div
      class="relative min-h-screen transition-all duration-300"
      :class="[sidebarCollapsed ? 'lg:ml-[72px]' : 'lg:ml-64']"
    >
      <!-- Header -->
      <AppHeader />

      <!-- Main Content -->
      <main class="p-4 md:p-6 lg:p-8">
        <slot />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import '@/styles/onboarding.css'
import { computed, inject, onMounted, onUnmounted, provide, ref } from 'vue'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingTour } from '@/composables/useOnboardingTour'
import { useOnboardingStore } from '@/stores/onboarding'
import DarkVideoBackground from '@/components/common/DarkVideoBackground.vue'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'

const APP_LAYOUT_CONTEXT = 'xiass-app-layout-context'
const hasParentLayout = inject<boolean>(APP_LAYOUT_CONTEXT, false)
const isRootLayout = !hasParentLayout

if (isRootLayout) {
  provide(APP_LAYOUT_CONTEXT, true)
}

const appStore = useAppStore()
const authStore = useAuthStore()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
const isAdmin = computed(() => authStore.user?.role === 'admin')

const { replayTour } = isRootLayout
  ? useOnboardingTour({
    storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
    autoStart: true
  })
  : { replayTour: () => undefined }

const onboardingStore = useOnboardingStore()

// ==================== Canvas 粒子动画 ====================

const appCanvasRef = ref<HTMLCanvasElement | null>(null)
let animationId = 0
let teardownParticles: (() => void) | undefined

interface Particle {
  x: number; y: number; vx: number; vy: number; radius: number; opacity: number
}

function initParticles(): () => void {
  const canvas = appCanvasRef.value
  if (!canvas) return () => undefined
  const ctx = canvas.getContext('2d')
  if (!ctx) return () => undefined

  const resize = () => {
    // Retina canvases do not need to redraw a decorative background at the
    // display's maximum density. Cap it to keep route transitions responsive.
    const dpr = Math.min(window.devicePixelRatio || 1, 1.5)
    canvas.width = window.innerWidth * dpr
    canvas.height = window.innerHeight * dpr
    canvas.style.width = window.innerWidth + 'px'
    canvas.style.height = window.innerHeight + 'px'
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
  }
  resize()
  window.addEventListener('resize', resize)

  // The previous 60-particle, 60fps network was an expensive full-screen
  // decoration. Keep the same visual language at a calmer refresh budget.
  const count = Math.min(36, Math.max(20, Math.floor(window.innerWidth / 36)))
  const particles: Particle[] = []
  const maxDist = 140
  const frameInterval = 1000 / 30
  let previousFrameAt = 0

  for (let i = 0; i < count; i++) {
    particles.push({
      x: Math.random() * window.innerWidth,
      y: Math.random() * window.innerHeight,
      vx: (Math.random() - 0.5) * 0.3,
      vy: (Math.random() - 0.5) * 0.3,
      radius: Math.random() * 1.5 + 0.3,
      opacity: Math.random() * 0.4 + 0.1,
    })
  }

  function draw(frameAt: number) {
    if (!ctx) return
    if (document.visibilityState !== 'visible') {
      animationId = 0
      return
    }
    if (frameAt - previousFrameAt < frameInterval) {
      animationId = requestAnimationFrame(draw)
      return
    }
    previousFrameAt = frameAt

    const w = window.innerWidth, h = window.innerHeight
    ctx.clearRect(0, 0, w, h)

    // 动态检测深浅色模式以适配粒子和连线的视觉对比度
    const isDark = document.documentElement.classList.contains('dark')
    const color = isDark ? { r: 14, g: 165, b: 233 } : { r: 37, g: 99, b: 235 }
    const lineOpacity = isDark ? 0.08 : 0.14
    const pOpacityMult = isDark ? 1.0 : 1.4

    for (const p of particles) {
      p.x += p.vx; p.y += p.vy
      if (p.x < 0 || p.x > w) p.vx *= -1
      if (p.y < 0 || p.y > h) p.vy *= -1
    }
    for (let i = 0; i < particles.length; i++) {
      for (let j = i + 1; j < particles.length; j++) {
        const dx = particles[i].x - particles[j].x, dy = particles[i].y - particles[j].y
        const dist = Math.sqrt(dx * dx + dy * dy)
        if (dist < maxDist) {
          ctx.beginPath()
          ctx.strokeStyle = `rgba(${color.r},${color.g},${color.b},${(1 - dist / maxDist) * lineOpacity})`
          ctx.lineWidth = 0.5
          ctx.moveTo(particles[i].x, particles[i].y)
          ctx.lineTo(particles[j].x, particles[j].y)
          ctx.stroke()
        }
      }
    }
    for (const p of particles) {
      ctx.beginPath()
      ctx.arc(p.x, p.y, p.radius, 0, Math.PI * 2)
      ctx.fillStyle = `rgba(${color.r},${color.g},${color.b},${p.opacity * pOpacityMult})`
      ctx.fill()
    }
    animationId = requestAnimationFrame(draw)
  }
  const resumeWhenVisible = () => {
    if (document.visibilityState === 'visible' && animationId === 0) {
      previousFrameAt = 0
      animationId = requestAnimationFrame(draw)
    }
  }
  document.addEventListener('visibilitychange', resumeWhenVisible)
  animationId = requestAnimationFrame(draw)

  return () => {
    cancelAnimationFrame(animationId)
    animationId = 0
    window.removeEventListener('resize', resize)
    document.removeEventListener('visibilitychange', resumeWhenVisible)
  }
}

onMounted(() => {
  if (!isRootLayout) return
  onboardingStore.setReplayCallback(replayTour)
  teardownParticles = initParticles()
})

onUnmounted(() => {
  if (!isRootLayout) return
  teardownParticles?.()
})

defineExpose({ replayTour })
</script>

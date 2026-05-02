<template>
  <div class="flex items-center gap-2 px-3 py-2 mb-2 rounded-lg bg-slate-50 dark:bg-slate-800/50 border border-slate-100 dark:border-slate-800">
    <div class="text-[10px] font-bold text-slate-400 uppercase tracking-widest mr-auto">Lang</div>
    <div class="flex gap-1">
      <button 
        v-for="locale in locales" 
        :key="locale.code"
        @click="setLocale(locale.code)"
        class="w-7 h-7 flex items-center justify-center rounded text-[10px] font-bold transition-all"
        :class="currentLocale === locale.code 
          ? 'bg-blue-600 text-white shadow-sm' 
          : 'text-slate-500 hover:bg-slate-200 dark:hover:bg-slate-700'"
      >
        {{ locale.code.toUpperCase() }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
const { locale: currentLocale, locales, setLocale } = useI18n()
</script>

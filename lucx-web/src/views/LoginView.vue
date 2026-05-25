<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import Message from 'primevue/message'

const router = useRouter()
const auth = useAuthStore()

const password = ref('')
const submitting = ref(false)

const submit = async () => {
  submitting.value = true
  try {
    await auth.login(password.value)
    router.replace('/')
  } catch {
    // error is set in store
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <div class="login-card">
      <h1>LucX</h1>
      <p class="subtitle">Multi-hop proxy orchestrator</p>

      <form @submit.prevent="submit">
        <label for="pass">Password</label>
        <InputText
          id="pass"
          v-model="password"
          type="password"
          placeholder="Enter JWT secret"
          autofocus
          fluid
        />

        <Message v-if="auth.error" severity="error" size="small">
          {{ auth.error }}
        </Message>

        <Button
          type="submit"
          label="Sign In"
          icon="pi pi-sign-in"
          :loading="submitting"
          fluid
        />
      </form>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--p-surface-ground);
}

.login-card {
  width: 360px;
  padding: 32px;
  background: var(--p-surface-card);
  border-radius: 12px;
  border: 1px solid var(--p-surface-border);
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.3);
}

h1 {
  margin: 0;
  font-size: 28px;
  font-weight: 800;
  color: var(--p-primary-color);
  text-align: center;
}

.subtitle {
  text-align: center;
  color: var(--p-text-muted-color);
  margin: 4px 0 24px;
  font-size: 13px;
}

form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

label {
  font-size: 13px;
  color: var(--p-text-muted-color);
}
</style>

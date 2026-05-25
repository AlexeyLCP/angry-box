import { defineStore } from 'pinia'
import { ref } from 'vue'
import { chainsApi } from '@/api/client'
import type { Chain } from '@/types/chain'

export const useChainsStore = defineStore('chains', () => {
  const chains = ref<Chain[]>([])
  const loading = ref(false)

  const fetchAll = async () => {
    loading.value = true
    try {
      chains.value = await chainsApi.list()
    } finally {
      loading.value = false
    }
  }

  const create = async (name: string, nodes: unknown[]) => {
    const chain = await chainsApi.create({ name, nodes })
    chains.value.unshift(chain)
    return chain
  }

  const apply = async (id: string) => {
    const res = await chainsApi.apply(id)
    const chain = chains.value.find((c) => c.id === id)
    if (chain) chain.status = 'active'
    return res
  }

  const rollback = async (id: string) => {
    await chainsApi.rollback(id)
    const chain = chains.value.find((c) => c.id === id)
    if (chain) chain.status = 'draft'
  }

  const remove = async (id: string) => {
    await chainsApi.delete(id)
    chains.value = chains.value.filter((c) => c.id !== id)
  }

  const validate = (id: string) => chainsApi.validate(id)

  const getConnectionLink = (id: string) => chainsApi.getConnectionLink(id)

  const getChain = (id: string) => chains.value.find((c) => c.id === id) ?? null

  return {
    chains,
    loading,
    fetchAll,
    create,
    apply,
    rollback,
    remove,
    validate,
    getConnectionLink,
    getChain,
  }
})

type OpenDialog = {
  id: symbol
  zIndex: number
  order: number
}

const openDialogs: OpenDialog[] = []
let openOrder = 0

const syncBodyScrollLock = () => {
  document.body.classList.toggle('modal-open', openDialogs.length > 0)
}

export const registerOpenDialog = (id: symbol, zIndex: number) => {
  const existing = openDialogs.find(dialog => dialog.id === id)
  if (existing) {
    existing.zIndex = zIndex
    return
  }
  openDialogs.push({ id, zIndex, order: ++openOrder })
  syncBodyScrollLock()
}

export const unregisterOpenDialog = (id: symbol) => {
  const index = openDialogs.findIndex(dialog => dialog.id === id)
  if (index !== -1) {
    openDialogs.splice(index, 1)
    syncBodyScrollLock()
  }
}

export const isTopmostDialog = (id: symbol) => {
  const topmost = openDialogs.reduce<OpenDialog | undefined>((current, dialog) => {
    if (!current || dialog.zIndex > current.zIndex) return dialog
    if (dialog.zIndex === current.zIndex && dialog.order > current.order) return dialog
    return current
  }, undefined)
  return topmost?.id === id
}

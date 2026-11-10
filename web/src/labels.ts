// Ekranda görünen Türkçe adlar. Anahtarlar API'nin durum değerleridir.
const campaignStates: Record<string, string> = {
  draft: 'taslak',
  generating: 'üretiliyor',
  generation_failed: 'üretim başarısız',
  in_review: 'incelemede',
  approved: 'onaylandı',
  sending: 'gönderiliyor',
  completed: 'tamamlandı',
  completed_with_failures: 'hatalarla tamamlandı',
  rejected: 'reddedildi',
  cancelled: 'iptal edildi',
}

const messageStates: Record<string, string> = {
  sent: 'gönderildi',
  sending: 'gönderiliyor',
  queued: 'sırada',
  retrying: 'yeniden deneniyor',
  failed: 'başarısız',
  dead: 'vazgeçildi',
  skipped: 'atlandı',
}

const channels: Record<string, string> = { sms: 'SMS', email: 'E-posta' }

export const campaignState = (s: string) => campaignStates[s] ?? s
export const messageState = (s: string) => messageStates[s] ?? s
export const channelName = (c: string) => channels[c] ?? c

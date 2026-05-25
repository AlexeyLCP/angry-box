const countryMap: Record<string, string> = {
  fi: '🇫🇮', nl: '🇳🇱', de: '🇩🇪', fr: '🇫🇷',
  in: '🇮🇳', us: '🇺🇸', gb: '🇬🇧', jp: '🇯🇵',
  sg: '🇸🇬', ru: '🇷🇺', br: '🇧🇷', ca: '🇨🇦',
  au: '🇦🇺', se: '🇸🇪', no: '🇳🇴', dk: '🇩🇰',
  ch: '🇨🇭', it: '🇮🇹', es: '🇪🇸', pl: '🇵🇱',
}

export function useGeoGuess(host: string): string {
  // Try matching known IPs / hostname patterns
  const lower = host.toLowerCase()
  // If host is an IP, try common cloud regions via reverse hints
  if (/^\d+\.\d+\.\d+\.\d+$/.test(host)) {
    // Check cached geo hints
    const geoHints: Record<string, string> = {
      '34.88': 'fi', '35.228': 'fi',  // Google Finland
      '34.6': 'nl',                    // Google Netherlands
      '35.200': 'in',                  // Google India
      '35.240': 'de',                  // Google Germany
      '34.76': 'be',                   // Google Belgium
      '35.187': 'fr',                  // Google France
      '34.88.118': 'fi',
      '34.88.164': 'fi',
      '34.88.71': 'fi',
      '31.56': 'ir',                   // Iran (test server)
    }
    for (const [prefix, cc] of Object.entries(geoHints)) {
      if (host.startsWith(prefix)) return countryMap[cc] ?? '🌐'
    }
    return '🌐'
  }
  // Match TLD patterns in hostnames
  for (const [cc, flag] of Object.entries(countryMap)) {
    if (lower.includes(`.${cc}`) || lower.endsWith(`-${cc}`) || lower.includes(`${cc}.`)) {
      return flag
    }
  }
  // Match known words in hostname
  const wordHints: Record<string, string> = { finland: 'fi', germany: 'de', france: 'fr', india: 'in', netherlands: 'nl', japan: 'jp', singapore: 'sg', london: 'gb', 'lucx': 'fi' }
  for (const [word, cc] of Object.entries(wordHints)) {
    if (lower.includes(word)) return countryMap[cc] ?? '🌐'
  }
  return '🌐'
}

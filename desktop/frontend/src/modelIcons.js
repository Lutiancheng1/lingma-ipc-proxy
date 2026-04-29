import autoIcon from 'bootstrap-icons/icons/shuffle.svg'
import claudeIcon from './assets/icons/claude.svg'
import gemmaIcon from './assets/icons/gemma.svg'
import kimiIcon from './assets/icons/kimi.svg'
import lingmaIcon from './assets/images/lingma-icon.png'
import minimaxIcon from './assets/icons/minimax.svg'
import openaiIcon from './assets/icons/openai.svg'
import qwenIcon from './assets/icons/qwen.svg'

const ICONS = [
  { match: ['auto', 'automatic', '自动'], src: autoIcon, color: '#2563eb' },
  { match: ['qwen', 'qwq'], src: qwenIcon, color: '#5b6ee1' },
  { match: ['kimi', 'moonshot'], src: kimiIcon, color: '#111827' },
  { match: ['minimax', 'abab'], src: minimaxIcon, color: '#1677ff' },
  { match: ['claude', 'anthropic'], src: claudeIcon, color: '#d97757' },
  { match: ['gpt', 'openai'], src: openaiIcon, color: '#10a37f' },
  { match: ['gemma', 'google'], src: gemmaIcon, color: '#4285f4' },
]

export function modelIcon(model) {
  const text = `${model?.id || ''} ${model?.name || ''}`.toLowerCase()
  const matched = ICONS.find((item) => item.match.some((keyword) => text.includes(keyword)))
  return matched || { src: lingmaIcon, color: '#2563eb' }
}

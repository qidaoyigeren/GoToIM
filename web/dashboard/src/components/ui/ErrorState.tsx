import { AlertCircle, RefreshCw } from 'lucide-react'

type Props = {
  message?: string
  onRetry?: () => void
}

export default function ErrorState({ message = '加载失败', onRetry }: Props) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="w-16 h-16 rounded-2xl bg-red-50 flex items-center justify-center mb-4">
        <AlertCircle size={28} className="text-red-400" />
      </div>
      <h3 className="text-base font-medium text-gray-700">{message}</h3>
      {onRetry && (
        <button
          onClick={onRetry}
          className="mt-3 inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-white border border-gray-200 text-sm font-medium text-gray-600 hover:bg-gray-50 transition-colors"
        >
          <RefreshCw size={14} />
          重试
        </button>
      )}
    </div>
  )
}

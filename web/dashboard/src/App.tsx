import { Navigate, Routes, Route } from 'react-router-dom'
import AppLayout from '@/components/layout/AppLayout'
import PurchasePage from '@/pages/PurchasePage'
import ChatPage from '@/pages/ChatPage'
import DeliveryPage from '@/pages/DeliveryPage'

export default function App() {
  return (
    <Routes>
      <Route element={<AppLayout />}>
        <Route path="/" element={<Navigate to="/purchase" replace />} />
        <Route path="/purchase" element={<PurchasePage />} />
        <Route path="/chat" element={<ChatPage />} />
        <Route path="/delivery" element={<DeliveryPage />} />
        <Route path="*" element={<Navigate to="/purchase" replace />} />
      </Route>
    </Routes>
  )
}

import { Component, type ErrorInfo, type ReactNode } from 'react'
import ErrorState from './ErrorState'

type Props = {
  children: ReactNode
}

type State = {
  hasError: boolean
}

export default class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false }

  static getDerivedStateFromError(): State {
    return { hasError: true }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('[Dashboard] render error', error, info)
  }

  render() {
    if (this.state.hasError) {
      return (
        <ErrorState
          message="This view failed to render. Please refresh after checking the backend response."
          onRetry={() => this.setState({ hasError: false })}
        />
      )
    }

    return this.props.children
  }
}

/**
 * Resolves on the next macrotask. Fetch mocks await it before answering so a
 * page's transient "Loading ..." state stays in the DOM until Testing Library's
 * MutationObserver (a microtask) has seen it. Without it the mock settles in
 * microtasks, and since routes are React.lazy (#52) the loading state can appear
 * and vanish between two findBy checks when the machine is busy.
 */
export const nextTask = () => new Promise<void>((resolve) => setTimeout(resolve, 0))

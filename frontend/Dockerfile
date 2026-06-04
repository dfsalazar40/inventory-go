# Stage 1: build
FROM node:22-alpine AS builder

WORKDIR /app

COPY package.json package-lock.json* ./
RUN npm ci

COPY . .
RUN npm run build

# Stage 2: serve with nginx
FROM nginx:1.27-alpine

COPY --from=builder /app/dist /usr/share/nginx/html

# Default nginx config is sufficient for a single-page app served on port 80.
# The backend API is accessed from the browser directly via VITE_API_URL.

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]

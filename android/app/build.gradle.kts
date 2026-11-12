plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.plugin.compose")
}

android {
    lint {
        // UI strings are intentionally inline (single-language private app).
        disable += setOf("SetTextI18n", "HardcodedText")
        // Pinned to the API level of the target phone (Android 16); not chasing newer SDKs.
        disable += setOf("OldTargetApi", "GradleDependency", "AndroidGradlePluginVersion")
        // A sideloaded app (not on Play) whose whole job is a long-lived connection: it asks to skip battery
        // optimizations on purpose.
        disable += "BatteryLife"
        warningsAsErrors = true
    }

    namespace = "dev.iyazerski.clipsync"
    compileSdk = 37

    defaultConfig {
        applicationId = "dev.iyazerski.clipsync"
        // 34 for TileService.startActivityAndCollapse(PendingIntent) and the specialUse service type.
        minSdk = 34
        targetSdk = 36
        // CI passes its run number so every build installs over the previous one.
        val build = providers.environmentVariable("BUILD_NUMBER").orNull?.toInt() ?: 1
        versionCode = build
        versionName = "0.1.$build"
    }

    // A fixed release key (from CI secrets) keeps the signature stable across builds, so updates install
    // in place. Local builds without it use the debug key.
    val keystore = providers.environmentVariable("CLIPSYNC_KEYSTORE").orNull
    signingConfigs {
        if (keystore != null) {
            create("release") {
                storeFile = file(keystore)
                storePassword = providers.environmentVariable("CLIPSYNC_KEYSTORE_PASSWORD").get()
                keyAlias = "clipsync"
                keyPassword = providers.environmentVariable("CLIPSYNC_KEYSTORE_PASSWORD").get()
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"))
            signingConfig = signingConfigs.getByName(if (keystore != null) "release" else "debug")
        }
    }

    buildFeatures {
        compose = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_21
        targetCompatibility = JavaVersion.VERSION_21
    }

    // Kotlin reflection metadata; the app doesn't use kotlin-reflect, so it's dead weight in the APK.
    packaging.resources.excludes += "kotlin/**.kotlin_builtins"
}

dependencies {
    implementation(platform("androidx.compose:compose-bom:2026.09.00"))
    implementation("androidx.compose.material3:material3")
    implementation("androidx.activity:activity-compose:1.13.0")
    // Liquid glass surfaces (refraction, blur, highlights) over the backdrop.
    implementation("dev.chrisbanes.haze:haze:2.1.0")
    implementation("dev.chrisbanes.haze:haze-glass:2.1.0")

    testImplementation("junit:junit:4.13.2")
}
